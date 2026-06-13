import * as React from "react";
import type { Task, TaskKind, ToolCardData } from "@/lib/types";
import * as api from "@/lib/api";
import { describeToolCall } from "@/lib/timeline";
import { type AppState, type ChatItem, initialState, uid } from "./types";
import { useToast } from "@/components/toast-host";

// orderedAssetIds returns the session's asset ids in timeline order (by real
// creation time, earliest first). Sent as `assetOrder` so the backend builds the
// "图N/视频N → id" map the agent uses to resolve user references. Timeline order
// is stable (no drag reorder), keeping the numbering anchor consistent.
function orderedAssetIds(s: AppState): string[] {
  const assets = [...s.assets.values()];
  assets.sort((a, b) => {
    const ta = a.createdAt ? Date.parse(a.createdAt) : 0;
    const tb = b.createdAt ? Date.parse(b.createdAt) : 0;
    return ta - tb;
  });
  return assets.map((a) => a.id);
}

// useAppController owns all app state and the real-time side effects (WS
// conversation + per-task SSE), mirroring the legacy app.js behavior.
export function useAppController() {
  const { toast } = useToast();
  const [state, setState] = React.useState<AppState>(initialState);
  const stateRef = React.useRef(state);
  stateRef.current = state;

  const wsRef = React.useRef<WebSocket | null>(null);
  const streamsRef = React.useRef<Map<string, EventSource>>(new Map());

  // Typewriter state for the in-flight assistant bubble and reasoning block.
  const typer = React.useRef({ id: "", target: "", shown: 0, done: false });
  const reasoner = React.useRef({ id: "", target: "", shown: 0 });
  const tickRef = React.useRef<number | null>(null);
  // producedRef tracks whether the current turn produced any visible output
  // (assistant text / tool call / capsule); reset on turn_start, consulted on
  // turn_end to decide whether an empty-reply fallback line is needed.
  const producedRef = React.useRef(false);
  // lastToolNoteRef holds the agent's humanized understanding of the most recent
  // tool call, stamped onto the task when its tool_result (with task_id) lands.
  const lastToolNoteRef = React.useRef<string | undefined>(undefined);
  const setChat = React.useCallback((fn: (c: ChatItem[]) => ChatItem[]) => {
    setState((s) => ({ ...s, chat: fn(s.chat) }));
  }, []);

  // ============ workspace data ============
  const refreshWorkspace = React.useCallback(async (sid: string) => {
    try {
      const [assets, tasks] = await Promise.all([api.listAssets(sid), api.listTasks(sid)]);
      setState((s) => ({
        ...s,
        assets: new Map(assets.map((a) => [a.id, a])),
        tasks: new Map(tasks.map((t) => [t.id, t])),
      }));
      subscribeRunningTasks(sid, tasks);
    } catch (e) {
      toast("工作区加载失败：" + (e as Error).message);
    }
  }, [toast]); // eslint-disable-line react-hooks/exhaustive-deps

  const refreshContext = React.useCallback(async (sid: string) => {
    try {
      const ctx = await api.getContext(sid);
      setState((s) => ({ ...s, context: ctx }));
    } catch {
      /* non-fatal */
    }
  }, []);

  // ============ SSE per-task streams ============
  const closeStream = React.useCallback((taskId: string) => {
    const es = streamsRef.current.get(taskId);
    if (es) {
      es.close();
      streamsRef.current.delete(taskId);
    }
  }, []);

  const applyTaskEvent = React.useCallback(
    (sid: string, taskId: string, type: string, data: Record<string, unknown>) => {
      const progress = typeof data.progress === "number" ? data.progress : undefined;
      setState((s) => {
        const tasks = new Map(s.tasks);
        const cur: Task = tasks.get(taskId) || { id: taskId, kind: "generate", status: "queued", progress: 0 };
        if (type === "task_queued") tasks.set(taskId, { ...cur, status: "queued" });
        else if (type === "task_running" || type === "task_progress")
          tasks.set(taskId, { ...cur, status: "running", progress: progress ?? cur.progress });
        else if (type === "task_done") tasks.set(taskId, { ...cur, status: "done", progress: 100 });
        else if (type === "task_failed")
          tasks.set(taskId, { ...cur, status: "failed", error: (data.error as string) || "生成失败" });
        return { ...s, tasks };
      });
      if (type === "task_done") {
        closeStream(taskId);
        void refreshWorkspace(sid);
        void refreshContext(sid);
      } else if (type === "task_failed") {
        closeStream(taskId);
        toast("有一个生成任务失败了，可在工作区重试", "warn");
      }
    },
    [closeStream, refreshWorkspace, refreshContext, toast],
  );

  const subscribeTask = React.useCallback(
    (sid: string, taskId: string) => {
      if (streamsRef.current.has(taskId)) return;
      const es = new EventSource(`/api/tasks/${taskId}/events`);
      streamsRef.current.set(taskId, es);
      const handle = (ev: MessageEvent) => {
        try {
          const evt = JSON.parse(ev.data);
          applyTaskEvent(sid, taskId, evt.type, evt.data || {});
        } catch {
          /* ignore */
        }
      };
      for (const n of ["task_queued", "task_running", "task_progress", "task_done", "task_failed"])
        es.addEventListener(n, handle as EventListener);
      es.onmessage = handle;
    },
    [applyTaskEvent],
  );

  function subscribeRunningTasks(sid: string, tasks: Task[]) {
    for (const t of tasks) {
      if (t.status === "done" || t.status === "failed") closeStream(t.id);
      else subscribeTask(sid, t.id);
    }
  }

  const ensureTaskPlaceholder = React.useCallback(
    (sid: string, taskId: string, kind: TaskKind, note?: string) => {
      setState((s) => {
        const existing = s.tasks.get(taskId);
        const tasks = new Map(s.tasks);
        if (existing) {
          // Backfill the note if newly known; otherwise leave the task as-is.
          if (note && !existing.note) tasks.set(taskId, { ...existing, note });
          else return s;
        } else {
          tasks.set(taskId, { id: taskId, kind, status: "running", progress: 0, note });
        }
        return { ...s, tasks };
      });
      subscribeTask(sid, taskId);
    },
    [subscribeTask],
  );

  // ============ chat typewriter ============
  // The assistant bubble and reasoning block both reveal characters at a fixed
  // cadence, decoupled from arrival rhythm. A single rAF-ish interval pumps both.
  const pump = React.useCallback(() => {
    if (tickRef.current != null) return;
    tickRef.current = window.setInterval(() => {
      let changed = false;
      const t = typer.current;
      const r = reasoner.current;
      if (t.id && t.shown < t.target.length) {
        const backlog = t.target.length - t.shown;
        t.shown = Math.min(t.target.length, t.shown + Math.max(2, Math.floor(backlog / 24)));
        changed = true;
      }
      if (r.id && r.shown < r.target.length) {
        const backlog = r.target.length - r.shown;
        r.shown = Math.min(r.target.length, r.shown + Math.max(2, Math.floor(backlog / 24)));
        changed = true;
      }
      if (changed) {
        setChat((c) =>
          c.map((it) => {
            if (it.kind === "assistant" && it.id === t.id) return { ...it, text: t.target.slice(0, t.shown) };
            if (it.kind === "reasoning" && it.id === r.id) return { ...it, text: r.target.slice(0, r.shown) };
            return it;
          }),
        );
      }
      const tDone = !t.id || t.shown >= t.target.length;
      const rDone = !r.id || r.shown >= r.target.length;
      if (tDone && rDone) {
        window.clearInterval(tickRef.current!);
        tickRef.current = null;
        if (t.done) {
          setChat((c) => c.map((it) => (it.kind === "assistant" && it.id === t.id ? { ...it, streaming: false } : it)));
          typer.current = { id: "", target: "", shown: 0, done: false };
        }
      }
    }, 16);
  }, [setChat]);

  const flushTyper = React.useCallback(() => {
    const t = typer.current;
    if (t.id) {
      setChat((c) => c.map((it) => (it.kind === "assistant" && it.id === t.id ? { ...it, text: t.target, streaming: false } : it)));
    }
    typer.current = { id: "", target: "", shown: 0, done: false };
  }, [setChat]);

  const collapseReasoning = React.useCallback(() => {
    const r = reasoner.current;
    if (!r.id) return;
    const id = r.id;
    const full = r.target;
    setChat((c) => c.map((it) => (it.kind === "reasoning" && it.id === id ? { ...it, text: full, collapsed: true, done: true } : it)));
    reasoner.current = { id: "", target: "", shown: 0 };
  }, [setChat]);

  // ============ turn lifecycle ============
  // A loading placeholder bubble is shown the instant a turn starts (locally on
  // send, and reaffirmed on the turn_start event) so the UI never sits blank
  // while the model spins up. It is removed as soon as real content arrives or
  // the turn ends.
  const showLoading = React.useCallback(() => {
    setState((s) => {
      if (s.chat.some((it) => it.kind === "loading")) return { ...s, thinking: true };
      return { ...s, thinking: true, chat: [...s.chat, { kind: "loading", id: uid("load") }] };
    });
  }, []);

  const clearLoading = React.useCallback(() => {
    setChat((c) => c.filter((it) => it.kind !== "loading"));
  }, [setChat]);

  const onAssistantDelta = React.useCallback((text: string, done: boolean) => {
    clearLoading();
    collapseReasoning();
    if (!typer.current.id) {
      const id = uid("a");
      typer.current = { id, target: "", shown: 0, done: false };
      setChat((c) => [...c, { kind: "assistant", id, text: "", streaming: true }]);
    }
    if (done) {
      if (text && text.length >= typer.current.target.length) typer.current.target = text;
      typer.current.done = true;
    } else if (text) {
      typer.current.target += text;
    }
    pump();
  }, [clearLoading, collapseReasoning, pump, setChat]);

  const onReasoning = React.useCallback((text: string) => {
    if (!text) return;
    clearLoading();
    flushTyper();
    if (!reasoner.current.id) {
      const id = uid("r");
      reasoner.current = { id, target: "", shown: 0 };
      setChat((c) => [...c, { kind: "reasoning", id, text: "", collapsed: false, done: false }]);
    }
    reasoner.current.target += text;
    pump();
  }, [clearLoading, flushTyper, pump, setChat]);

  // ============ tool cards ============
  const onToolCall = React.useCallback((data: Record<string, unknown>) => {
    clearLoading();
    flushTyper();
    collapseReasoning();
    let args: Record<string, unknown> | undefined;
    if (typeof data.arguments === "string") {
      try { args = JSON.parse(data.arguments); } catch { args = undefined; }
    }
    const card: ToolCardData = {
      id: (data.id as string) || uid("t"),
      name: (data.name as string) || "tool",
      args,
      status: "running",
    };
    // Capture the agent's understanding of this op so the timeline node can show
    // it once the tool_result (which carries the task_id) arrives.
    lastToolNoteRef.current = describeToolCall(card.name, args);
    setChat((c) => [...c, { kind: "tool", id: card.id!, tool: card }]);
  }, [flushTyper, collapseReasoning, setChat]);

  const onToolResult = React.useCallback((sid: string, data: Record<string, unknown>) => {
    const ok = data.status !== "error";
    const name = data.name as string | undefined;
    if (ok && typeof data.task_id === "string") {
      ensureTaskPlaceholder(sid, data.task_id, ((data.kind as TaskKind) || "generate"), lastToolNoteRef.current);
      lastToolNoteRef.current = undefined;
    }
    setChat((c) => {
      // complete the most recent running card matching the name (or any).
      const idx = [...c].reverse().findIndex((it) => it.kind === "tool" && it.tool.status === "running" && (!name || it.tool.name === name));
      if (idx < 0) return c;
      const real = c.length - 1 - idx;
      return c.map((it, i) =>
        i === real && it.kind === "tool"
          ? { ...it, tool: { ...it.tool, status: ok ? "done" : "failed", summary: data.summary as string, error: data.error as string } }
          : it,
      );
    });
  }, [ensureTaskPlaceholder, setChat]);

  const finishPendingTools = React.useCallback(() => {
    setChat((c) => c.map((it) => (it.kind === "tool" && it.tool.status === "running" ? { ...it, tool: { ...it.tool, status: "done" } } : it)));
  }, [setChat]);

  // sendNow performs the actual WS send for one user message: it appends the
  // user bubble, shows the loading state, and emits the user_message frame with
  // the current asset display order. Callers gate on `thinking` before calling.
  const sendNow = React.useCallback((value: string, ref?: string | string[]) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      toast("连接尚未就绪，请稍候");
      return;
    }
    setChat((c) => [...c, { kind: "user", id: uid("u"), text: value }]);
    showLoading();
    const payload: Record<string, unknown> = { type: "user_message", text: value, lossless: stateRef.current.lossless };
    const order = orderedAssetIds(stateRef.current);
    if (order.length) payload.assetOrder = order;
    if (Array.isArray(ref)) {
      if (ref.length === 1) payload.ref = ref[0];
      else if (ref.length > 1) payload.refs = ref.slice(0, 6);
    } else if (ref) {
      payload.ref = ref;
    }
    ws.send(JSON.stringify(payload));
  }, [setChat, toast, showLoading]);

  const onTurnEnd = React.useCallback((sid: string, data: Record<string, unknown>) => {
    clearLoading();
    setState((s) => ({ ...s, thinking: false }));
    flushTyper();
    finishPendingTools();
    // Empty-reply fallback: if the turn produced no body text, no tool, and no
    // capsule, surface a short standby line so the user never sees "thought but
    // said nothing". The backend suppresses the empty done-message; this guards
    // the UI side per the connectWS-tracked produced flag.
    const replyEmpty = data.replyEmpty === true;
    const toolUsed = data.toolUsed === true;
    const hasCapsule = data.hasCapsule === true;
    const cancelled = data.cancelled === true;
    if (replyEmpty && !toolUsed && !hasCapsule && !cancelled && !producedRef.current) {
      setChat((c) => [...c, { kind: "assistant", id: uid("a"), text: "我在的，有什么宣发素材需要我帮你处理吗？", streaming: false }]);
    }
    // Prefer the context snapshot carried on turn_end; fall back to a fetch.
    const ctx = data.context as AppState["context"] | undefined;
    if (ctx && typeof ctx.estimatedTokens === "number") {
      setState((s) => ({ ...s, context: ctx }));
    } else {
      void refreshContext(sid);
    }
    // Auto-flush the next queued message, if any.
    const next = stateRef.current.queue[0];
    if (next) {
      setState((s) => ({ ...s, queue: s.queue.slice(1) }));
      // Defer so the thinking=false / queue update lands before the next send.
      setTimeout(() => sendNow(next.text, next.ref), 0);
    }
  }, [clearLoading, flushTyper, finishPendingTools, refreshContext, setChat, sendNow]);

  const onCapsule = React.useCallback((data: Record<string, unknown>) => {
    clearLoading();
    setState((s) => ({ ...s, thinking: false }));
    flushTyper();
    const question = (data.question as string) || "请选择";
    const rawOpts = Array.isArray(data.options) ? (data.options as Record<string, unknown>[]) : [];
    const options = rawOpts.map((o) => ({
      label: (o.label as string) || (o.value as string) || "",
      value: (o.value as string) || (o.label as string) || "",
      editableHint: (o.editable_hint as string) || (o.editableHint as string) || undefined,
    }));
    setChat((c) => [...c, { kind: "capsule", id: uid("cap"), question, options, answered: false }]);
  }, [clearLoading, flushTyper, setChat]);

  // ============ WebSocket ============
  const connectWS = React.useCallback((sid: string) => {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(`${proto}://${location.host}/api/ws?session=${encodeURIComponent(sid)}`);
    wsRef.current = ws;
    ws.onopen = () => setState((s) => ({ ...s, connected: true }));
    ws.onclose = () => {
      setState((s) => ({ ...s, connected: false }));
      setTimeout(() => connectWS(sid), 1500);
    };
    ws.onerror = () => ws.close();
    ws.onmessage = (ev) => {
      let msg: { type: string; data?: Record<string, unknown> };
      try { msg = JSON.parse(ev.data); } catch { return; }
      const d = msg.data || {};
      switch (msg.type) {
        case "turn_start":
          producedRef.current = false;
          showLoading();
          break;
        case "turn_end":
          onTurnEnd(sid, d);
          break;
        case "capsule":
          producedRef.current = true;
          onCapsule(d);
          break;
        case "message":
          if ((d.text as string) || "") producedRef.current = true;
          onAssistantDelta((d.text as string) || "", !!d.done);
          if (d.done) { finishPendingTools(); void refreshContext(sid); }
          break;
        case "reasoning":
          onReasoning((d.text as string) || "");
          break;
        case "tool_call":
          producedRef.current = true;
          onToolCall(d);
          break;
        case "tool_result":
          onToolResult(sid, d);
          break;
        case "task_created":
          if (typeof d.task_id === "string") ensureTaskPlaceholder(sid, d.task_id, (d.kind as TaskKind) || "generate");
          break;
        case "error":
          clearLoading();
          setState((s) => ({ ...s, thinking: false }));
          toast((d.message as string) || "发生未知错误");
          break;
      }
    };
  }, [onAssistantDelta, onReasoning, onToolCall, onToolResult, ensureTaskPlaceholder, finishPendingTools, refreshContext, toast, showLoading, onTurnEnd, onCapsule, clearLoading]);

  // ============ actions ============
  // sendMessage routes a user input: when a turn is in flight it joins the
  // pending queue (auto-flushed on turn_end); otherwise it sends immediately.
  const sendMessage = React.useCallback((text: string, ref?: string | string[]) => {
    const value = text.trim();
    if (!value) return;
    if (stateRef.current.thinking) {
      setState((s) => ({ ...s, queue: [...s.queue, { id: uid("q"), text: value, ref }] }));
      return;
    }
    sendNow(value, ref);
  }, [sendNow]);

  // promoteQueued moves a queued message to the front so it is the next to send.
  const promoteQueued = React.useCallback((id: string) => {
    setState((s) => {
      const idx = s.queue.findIndex((q) => q.id === id);
      if (idx <= 0) return s;
      const q = [...s.queue];
      const [item] = q.splice(idx, 1);
      return { ...s, queue: [item, ...q] };
    });
  }, []);

  // removeQueued drops a queued message before it is sent.
  const removeQueued = React.useCallback((id: string) => {
    setState((s) => ({ ...s, queue: s.queue.filter((q) => q.id !== id) }));
  }, []);

  // interruptSend cancels the in-flight turn and immediately sends a message:
  // either the given queued message, or a fresh text. It dequeues the chosen
  // message and sends a cancel_turn before the new user_message so the backend
  // aborts the old turn first.
  const interruptSend = React.useCallback((arg: { id?: string; text?: string; ref?: string | string[] }) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      toast("连接尚未就绪，请稍候");
      return;
    }
    let value = (arg.text || "").trim();
    let ref = arg.ref;
    if (arg.id) {
      const item = stateRef.current.queue.find((q) => q.id === arg.id);
      if (item) { value = item.text; ref = item.ref; }
      setState((s) => ({ ...s, queue: s.queue.filter((q) => q.id !== arg.id) }));
    }
    if (!value) return;
    // Abort the running turn, then send the new message. The backend serializes
    // on the per-session turn lock, so the cancelled turn releases before the
    // new one starts.
    ws.send(JSON.stringify({ type: "cancel_turn" }));
    setState((s) => ({ ...s, thinking: false }));
    sendNow(value, ref);
  }, [sendNow, toast]);

  // capsuleSelect answers a clarify capsule: it marks the bubble answered, shows
  // a user echo of the chosen/edited text, and sends it back over the WS so the
  // agent continues the conversation. value is the (possibly edited) text.
  const capsuleSelect = React.useCallback((capsuleId: string, value: string) => {
    const text = value.trim();
    if (!text) return;
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      toast("连接尚未就绪，请稍候");
      return;
    }
    setChat((c) =>
      c.map((it) => (it.kind === "capsule" && it.id === capsuleId ? { ...it, answered: true } : it)),
    );
    setChat((c) => [...c, { kind: "user", id: uid("u"), text }]);
    showLoading();
    const csPayload: Record<string, unknown> = { type: "capsule_select", text, lossless: stateRef.current.lossless };
    const csOrder = orderedAssetIds(stateRef.current);
    if (csOrder.length) csPayload.assetOrder = csOrder;
    ws.send(JSON.stringify(csPayload));
  }, [setChat, toast, showLoading]);

  // ============ boot ============
  React.useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const sid = await api.bootSession();
        if (!alive) return;
        setState((s) => ({ ...s, sessionId: sid }));
        connectWS(sid);
        await refreshWorkspace(sid);
        await refreshContext(sid);
      } catch (e) {
        toast("会话初始化失败：" + (e as Error).message);
      }
    })();
    return () => {
      alive = false;
      wsRef.current?.close();
      streamsRef.current.forEach((es) => es.close());
      if (tickRef.current != null) window.clearInterval(tickRef.current);
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // ============ workspace actions ============
  const toggleSelect = React.useCallback((id: string) => {
    setState((s) => {
      const sel = new Set(s.selected);
      if (sel.has(id)) sel.delete(id);
      else sel.add(id);
      return { ...s, selected: sel };
    });
  }, []);

  const selectAll = React.useCallback(() => {
    setState((s) => ({ ...s, selected: new Set(s.assets.keys()) }));
  }, []);

  const clearSelection = React.useCallback(() => {
    setState((s) => ({ ...s, selected: new Set() }));
  }, []);

  const setLossless = React.useCallback((v: boolean) => {
    setState((s) => ({ ...s, lossless: v }));
  }, []);

  const removeAsset = React.useCallback(async (assetId: string) => {
    const sid = stateRef.current.sessionId;
    try {
      await api.deleteAsset(sid, assetId);
      setState((s) => {
        const assets = new Map(s.assets);
        assets.delete(assetId);
        const sel = new Set(s.selected);
        sel.delete(assetId);
        return { ...s, assets, selected: sel };
      });
      toast("已移除", "ok");
    } catch (e) {
      toast("移除失败：" + (e as Error).message);
    }
  }, [toast]);

  const removeSelected = React.useCallback(async () => {
    const sid = stateRef.current.sessionId;
    const ids = [...stateRef.current.selected];
    if (ids.length === 0) return;
    const results = await Promise.allSettled(ids.map((id) => api.deleteAsset(sid, id)));
    const ok = ids.filter((_, i) => results[i].status === "fulfilled");
    setState((s) => {
      const assets = new Map(s.assets);
      const sel = new Set(s.selected);
      for (const id of ok) { assets.delete(id); sel.delete(id); }
      return { ...s, assets, selected: sel };
    });
    const failed = ids.length - ok.length;
    if (failed > 0) toast(`移除 ${ok.length} 张，${failed} 张失败`);
    else toast(`已移除 ${ok.length} 张`, "ok");
  }, [toast]);

  const removeTask = React.useCallback(async (taskId: string) => {
    const sid = stateRef.current.sessionId;
    const t = stateRef.current.tasks.get(taskId);
    const inFlight = t?.status === "queued" || t?.status === "running";
    try {
      await api.deleteTask(sid, taskId);
      closeStream(taskId);
      setState((s) => {
        const tasks = new Map(s.tasks);
        tasks.delete(taskId);
        return { ...s, tasks };
      });
      toast(inFlight ? "已取消任务" : "已移除", "ok");
    } catch (e) {
      toast((inFlight ? "取消失败：" : "移除失败：") + (e as Error).message);
    }
  }, [closeStream, toast]);

  const clearFailed = React.useCallback(async () => {
    const sid = stateRef.current.sessionId;
    try {
      await api.clearFailedTasks(sid);
      setState((s) => {
        const tasks = new Map(s.tasks);
        for (const [id, t] of tasks) if (t.status === "failed") tasks.delete(id);
        return { ...s, tasks };
      });
      toast("已清除失败任务", "ok");
    } catch (e) {
      toast("清除失败：" + (e as Error).message);
    }
  }, [toast]);

  const retryTask = React.useCallback(async (taskId: string) => {
    const sid = stateRef.current.sessionId;
    try {
      await api.retryTask(sid, taskId);
      setState((s) => {
        const tasks = new Map(s.tasks);
        const t = tasks.get(taskId);
        if (t) tasks.set(taskId, { ...t, status: "queued", error: undefined });
        return { ...s, tasks };
      });
      subscribeTask(sid, taskId);
    } catch (e) {
      toast("重试失败：" + (e as Error).message);
    }
  }, [subscribeTask, toast]);

  const clearWorkspace = React.useCallback(async () => {
    const sid = stateRef.current.sessionId;
    try {
      await api.clearWorkspace(sid);
      setState((s) => ({ ...s, selected: new Set() }));
      await refreshWorkspace(sid);
      toast("工作区已清空", "ok");
    } catch (e) {
      toast("清空失败：" + (e as Error).message);
    }
  }, [refreshWorkspace, toast]);

  const clearContext = React.useCallback(async () => {
    const sid = stateRef.current.sessionId;
    try {
      await api.clearContext(sid);
      setState((s) => ({ ...s, chat: [] }));
      typer.current = { id: "", target: "", shown: 0, done: false };
      reasoner.current = { id: "", target: "", shown: 0 };
      void refreshContext(sid);
      toast("上下文已清理", "ok");
    } catch (e) {
      toast("清理失败：" + (e as Error).message);
    }
  }, [refreshContext, toast]);

  const uploadFiles = React.useCallback(async (files: FileList | File[]) => {
    const sid = stateRef.current.sessionId;
    const list = [...files].filter((f) => f && f.type.startsWith("image/"));
    if (!list.length) return;
    const results = await Promise.allSettled(list.map((f) => api.uploadFile(sid, f)));
    setState((s) => {
      const assets = new Map(s.assets);
      for (const r of results) if (r.status === "fulfilled") assets.set(r.value.id, r.value);
      return { ...s, assets };
    });
    const ok = results.filter((r) => r.status === "fulfilled").length;
    const fail = results.length - ok;
    if (fail === 0) toast(ok === 1 ? "已上传，现在可以让我换背景/角色/文案" : `已上传 ${ok} 张图`, "ok");
    else if (ok === 0) toast(`上传失败 ${fail} 张`, "error");
    else toast(`已上传 ${ok} 张，失败 ${fail} 张`, "warn");
  }, [toast]);

  return {
    state,
    setState,
    sendMessage,
    capsuleSelect,
    promoteQueued,
    removeQueued,
    interruptSend,
    refreshWorkspace,
    refreshContext,
    subscribeTask,
    closeStream,
    toggleSelect,
    selectAll,
    clearSelection,
    setLossless,
    removeAsset,
    removeSelected,
    removeTask,
    clearFailed,
    retryTask,
    clearWorkspace,
    clearContext,
    uploadFiles,
    toast,
    collapseReasoningItem: (id: string) =>
      setChat((c) => c.map((it) => (it.kind === "reasoning" && it.id === id ? { ...it, collapsed: !it.collapsed } : it))),
  } as const;
}
