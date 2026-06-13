import * as React from "react";
import type { Task, TaskKind, ToolCardData } from "@/lib/types";
import * as api from "@/lib/api";
import { type AppState, type ChatItem, initialState, uid } from "./types";
import { useToast } from "@/components/toast-host";

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
    (sid: string, taskId: string, kind: TaskKind) => {
      setState((s) => {
        if (s.tasks.has(taskId)) return s;
        const tasks = new Map(s.tasks);
        tasks.set(taskId, { id: taskId, kind, status: "running", progress: 0 });
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

  const onAssistantDelta = React.useCallback((text: string, done: boolean) => {
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
  }, [collapseReasoning, pump, setChat]);

  const onReasoning = React.useCallback((text: string) => {
    if (!text) return;
    flushTyper();
    if (!reasoner.current.id) {
      const id = uid("r");
      reasoner.current = { id, target: "", shown: 0 };
      setChat((c) => [...c, { kind: "reasoning", id, text: "", collapsed: false, done: false }]);
    }
    reasoner.current.target += text;
    pump();
  }, [flushTyper, pump, setChat]);

  // ============ tool cards ============
  const onToolCall = React.useCallback((data: Record<string, unknown>) => {
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
    setChat((c) => [...c, { kind: "tool", id: card.id!, tool: card }]);
  }, [flushTyper, collapseReasoning, setChat]);

  const onToolResult = React.useCallback((sid: string, data: Record<string, unknown>) => {
    const ok = data.status !== "error";
    const name = data.name as string | undefined;
    if (ok && typeof data.task_id === "string") {
      ensureTaskPlaceholder(sid, data.task_id, ((data.kind as TaskKind) || "generate"));
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
        case "message":
          onAssistantDelta((d.text as string) || "", !!d.done);
          if (d.done) { finishPendingTools(); void refreshContext(sid); }
          break;
        case "reasoning":
          onReasoning((d.text as string) || "");
          break;
        case "tool_call":
          onToolCall(d);
          break;
        case "tool_result":
          onToolResult(sid, d);
          break;
        case "task_created":
          if (typeof d.task_id === "string") ensureTaskPlaceholder(sid, d.task_id, (d.kind as TaskKind) || "generate");
          break;
        case "error":
          toast((d.message as string) || "发生未知错误");
          break;
      }
    };
  }, [onAssistantDelta, onReasoning, onToolCall, onToolResult, ensureTaskPlaceholder, finishPendingTools, refreshContext, toast]);

  // ============ actions ============
  const sendMessage = React.useCallback((text: string, ref?: string | string[]) => {
    const value = text.trim();
    if (!value) return;
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      toast("连接尚未就绪，请稍候");
      return;
    }
    setChat((c) => [...c, { kind: "user", id: uid("u"), text: value }]);
    const payload: Record<string, unknown> = { type: "user_message", text: value, lossless: stateRef.current.lossless };
    if (Array.isArray(ref)) {
      if (ref.length === 1) payload.ref = ref[0];
      else if (ref.length > 1) payload.refs = ref.slice(0, 6);
    } else if (ref) {
      payload.ref = ref;
    }
    ws.send(JSON.stringify(payload));
  }, [setChat, toast]);

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

  const setOrder = React.useCallback((order: string[]) => {
    setState((s) => ({ ...s, order }));
  }, []);

  const reorderAsset = React.useCallback((draggedId: string, targetId: string, after: boolean) => {
    setState((s) => {
      // Build the current effective order (display order with new ids appended).
      const seen = new Set<string>();
      const cur: string[] = [];
      for (const id of s.order) if (s.assets.has(id)) { cur.push(id); seen.add(id); }
      for (const a of s.assets.values()) if (!seen.has(a.id)) cur.push(a.id);
      const from = cur.indexOf(draggedId);
      if (from < 0) return s;
      cur.splice(from, 1);
      let to = cur.indexOf(targetId);
      if (to < 0) return s;
      if (after) to += 1;
      cur.splice(to, 0, draggedId);
      return { ...s, order: cur };
    });
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
    try {
      await api.deleteTask(sid, taskId);
      closeStream(taskId);
      setState((s) => {
        const tasks = new Map(s.tasks);
        tasks.delete(taskId);
        return { ...s, tasks };
      });
    } catch (e) {
      toast("移除失败：" + (e as Error).message);
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
    refreshWorkspace,
    refreshContext,
    subscribeTask,
    closeStream,
    toggleSelect,
    selectAll,
    clearSelection,
    setLossless,
    setOrder,
    reorderAsset,
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
