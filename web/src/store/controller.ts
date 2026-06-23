import * as React from "react";
import type { Task, TaskKind, ToolCardData } from "@/lib/types";
import * as api from "@/lib/api";
import { describeToolCall } from "@/lib/timeline";
import { type AppState, type ChatItem, initialState, uid } from "./types";
import { useToast } from "@/components/toast-host";

// MAX_SELECTED bounds how many assets can be selected as references at once,
// mirroring the backend's MaxReferenceImages. Selection beyond this is rejected
// (rather than silently truncated at send time) so the UI count never lies.
export const MAX_SELECTED = 6;

// SELECTED_KEY persists the reference selection across reloads. The chat "已选 N
// 张" count is client-only state, so without this a refresh would silently drop
// the user's pick. Restored on boot, filtered to assets that still exist.
const SELECTED_KEY = "gas.selected";

// ADAPT_PANELS_KEY persists the in-flight "适配流程" pipeline panels across reloads.
// These chat items are inserted only on the live tool_result event, so a refresh
// mid-adaptation would otherwise drop the panel while the tasks keep running. We
// stash {id, taskIds} per session and rebuild the panel on boot, keeping only
// panels that still have an unfinished (queued/running) task.
const ADAPT_PANELS_KEY = "gas.adaptPanels";
const adaptPanelsKey = (sid: string) => `${ADAPT_PANELS_KEY}:${sid}`;

type StashedAdaptPanel = { id: string; taskIds: string[] };

function loadAdaptPanels(sid: string): StashedAdaptPanel[] {
  try {
    const raw = sessionStorage.getItem(adaptPanelsKey(sid));
    const arr = raw ? JSON.parse(raw) : [];
    return Array.isArray(arr) ? arr : [];
  } catch {
    return [];
  }
}

function saveAdaptPanels(sid: string, panels: StashedAdaptPanel[]) {
  try {
    if (panels.length) sessionStorage.setItem(adaptPanelsKey(sid), JSON.stringify(panels));
    else sessionStorage.removeItem(adaptPanelsKey(sid));
  } catch {
    /* ignore quota/serialization errors */
  }
}

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

// SYNC_ASSET_TOOLS are tools that produce workspace assets synchronously within
// the agent turn (no async task / no task_done event). Their tool_result is the
// only signal that new assets exist, so the controller refreshes the workspace
// on result. Async tools (edit_image / generate_icon / image_to_video / crawl)
// are NOT listed here — they carry a task_id and refresh on task_done.
const SYNC_ASSET_TOOLS = new Set<string>(["crop_to_sizes", "overlay_text"]);

// variantDimensionLabel maps the generate_variants dimension key to its Chinese
// label for the grouped variants card.
function variantDimensionLabel(dim: string): string {
  switch (dim) {
    case "palette":
      return "配色";
    case "composition":
      return "构图";
    case "copy":
      return "文案侧重";
    case "style":
    default:
      return "风格";
  }
}

// useAppController owns all app state and the real-time side effects (WS
// conversation + per-task SSE), mirroring the legacy app.js behavior.
export function useAppController() {
  const { toast } = useToast();
  const [state, setState] = React.useState<AppState>(initialState);
  const stateRef = React.useRef(state);
  stateRef.current = state;

  // Persist the reference selection so a reload restores the chat "已选 N 张"
  // count (see SELECTED_KEY). Mirrors every change made via toggle/selectAll/
  // upload/sticky-select without threading writes through each callsite. Gated on
  // restoredRef so the initial empty-Set render does NOT clobber the persisted
  // value before boot's async restore reads it.
  const restoredRef = React.useRef(false);
  React.useEffect(() => {
    if (!restoredRef.current) return;
    try {
      sessionStorage.setItem(SELECTED_KEY, JSON.stringify([...state.selected]));
    } catch { /* ignore quota/availability errors */ }
  }, [state.selected]);

  const wsRef = React.useRef<WebSocket | null>(null);
  const streamsRef = React.useRef<Map<string, EventSource>>(new Map());

  // Typewriter state for the in-flight assistant bubble and reasoning block.
  const typer = React.useRef({ id: "", target: "", shown: 0, done: false });
  const reasoner = React.useRef({ id: "", target: "", shown: 0 });
  const tickRef = React.useRef<number | null>(null);
  // P2 escalation timer: when a turn enters P1 (turn_start, no increment yet),
  // we arm a ~1.5s timer that escalates the wait bubble to the static P2
  // fallback if no message/reasoning increment has arrived. Cleared on the first
  // increment, on turn_end/error/cancel, and on an explicit backend non-streaming
  // signal (which escalates to P2 immediately).
  const waitTimerRef = React.useRef<number | null>(null);
  // producedRef tracks whether the current turn produced any visible output
  // (assistant text / tool call / capsule); reset on turn_start, consulted on
  // turn_end to decide whether an empty-reply fallback line is needed.
  const producedRef = React.useRef(false);
  // lastToolNoteRef holds the agent's humanized understanding of the most recent
  // tool call, stamped onto the task when its tool_result (with task_id) lands.
  const lastToolNoteRef = React.useRef<string | undefined>(undefined);
  // pendingFollowUpRef stashes a follow_up payload that arrived while async tasks
  // (generate / video / search) were still in flight. The backend emits follow_up
  // at turn-end, but for async tasks the turn ends the instant the task is
  // submitted — long before the artifact exists — so a "已完成" bubble would lie.
  // We hold it and render only once the session has no queued/running tasks left.
  const pendingFollowUpRef = React.useRef<{ message: string; options: { label: string; value: string }[] } | null>(null);
  // retryCardsRef maps taskId → chat tool-card id for direct retries so the
  // tool card tracks task_done/task_failed without going through the agent WS.
  const retryCardsRef = React.useRef<Map<string, string>>(new Map());
  // pendingAdaptRef tracks task IDs collected during an adapt_to_platform tool
  // call so we can create the pipeline timeline chat item when the tool ends.
  const pendingAdaptRef = React.useRef<string[]>([]);
  // pendingVariantsRef tracks task IDs collected during a generate_variants tool
  // call (an async ReturnDirectly tool whose friendly ack is the model reply, so
  // its structured task_ids never ride the tool_result event — we instead collect
  // the per-variant task_created events here, mirroring pendingAdaptRef). dimension
  // is captured from the tool_call args so the group card can label the batch.
  const pendingVariantsRef = React.useRef<{ taskIds: string[]; dimension: string }>({ taskIds: [], dimension: "" });
  const setChat = React.useCallback((fn: (c: ChatItem[]) => ChatItem[]) => {
    setState((s) => ({ ...s, chat: fn(s.chat) }));
  }, []);

  // renderFollowUp appends a follow_up bubble to the chat. Used both for the
  // immediate case (no async task in flight) and the deferred case (flushed once
  // the session's tasks all settle — see pendingFollowUpRef / applyTaskEvent).
  const renderFollowUp = React.useCallback((message: string, options: { label: string; value: string }[]) => {
    setChat((c) => [...c, { kind: "follow_up", id: uid("fu"), message, options, dismissed: false }]);
  }, [setChat]);

  // sessionHasPendingTask reports whether any task is still queued/running (i.e.
  // an artifact is still being produced), optionally ignoring one task id (the one
  // that just transitioned, whose new terminal status may not be in `tasks` yet).
  const sessionHasPendingTask = (tasks: Map<string, Task>, ignoreID?: string) => {
    for (const [id, t] of tasks) {
      if (id === ignoreID) continue;
      if (t.status === "queued" || t.status === "running") return true;
    }
    return false;
  };

  // ============ workspace data ============
  const refreshWorkspace = React.useCallback(async (sid: string) => {
    try {
      const [assets, tasks] = await Promise.all([api.listAssets(sid), api.listTasks(sid)]);
      setState((s) => {
        // Preserve client-only task fields the /tasks API does not return.
        // IMPORTANT: never downgrade status — a stale API snapshot (e.g. fetched
        // while a task was still running) must not revert a done/failed task back
        // to running. done/failed=2 > running=1 > queued=0.
        const STATUS_ORD: Record<string, number> = { queued: 0, running: 1, done: 2, failed: 2 };
        const prev = s.tasks;
        const merged = new Map(
          tasks.map((t) => {
            const old = prev.get(t.id);
            if (!old) return [t.id, t] as const;
            const forward = (STATUS_ORD[t.status] ?? 0) >= (STATUS_ORD[old.status] ?? 0);
            const base = forward ? t : { ...t, status: old.status, progress: old.progress };
            return [t.id, { ...base, count: base.count ?? old.count, note: base.note ?? old.note, outpainted: old.outpainted, review: old.review, reviewReason: old.reviewReason, sizeId: base.sizeId ?? old.sizeId }] as const;
          }),
        );
        // Merge assets (spread existing + incoming) so a stale concurrent refresh
        // doesn't erase assets that a newer refresh already added to state.
        const merged_assets = new Map([...s.assets, ...assets.map((a) => [a.id, a] as const)]);
        return { ...s, assets: merged_assets, tasks: merged };
      });
      subscribeRunningTasks(sid, tasks);
      return tasks;
    } catch (e) {
      toast("工作区加载失败：" + (e as Error).message);
      return [];
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
      // sizeId rides the task_queued event for adapt tasks (Start/Retry). Capture
      // it so a later task_failed can be mapped back to the stamp-album slot; once
      // set it persists via the {...cur} spread across subsequent events.
      const sizeId = typeof data.sizeId === "string" ? data.sizeId : undefined;
      // Capture the task kind from the last committed state BEFORE the enqueued
      // setState below — the task_done auto-select needs it, and reading it after
      // setState would race the not-yet-applied update.
      const taskKind = stateRef.current.tasks.get(taskId)?.kind;
      setState((s) => {
        const tasks = new Map(s.tasks);
        const cur: Task = tasks.get(taskId) || { id: taskId, kind: "generate", status: "queued", progress: 0 };
        if (sizeId && cur.sizeId !== sizeId) cur.sizeId = sizeId;
        if (type === "task_queued") tasks.set(taskId, { ...cur, status: "queued" });
        else if (type === "task_running" || type === "task_progress")
          tasks.set(taskId, { ...cur, status: "running", progress: progress ?? cur.progress });
        else if (type === "task_done") tasks.set(taskId, { ...cur, status: "done", progress: 100 });
        else if (type === "task_failed")
          tasks.set(taskId, { ...cur, status: "failed", error: (data.error as string) || "生成失败" });
        // Quality-gate review sub-states (platform adaptation). Non-terminal: they
        // evolve the SAME placeholder card and never close the stream. The task
        // still ends with task_done/task_failed.
        else if (type === "review_started")
          tasks.set(taskId, { ...cur, status: "running", stage: "reviewing", review: "checking" });
        else if (type === "review_passed")
          tasks.set(taskId, { ...cur, stage: undefined, review: "passed" });
        else if (type === "review_failed")
          tasks.set(taskId, {
            ...cur,
            status: "running",
            stage: undefined,
            review: "failed",
            reviewReason: Array.isArray(data.reasons) ? (data.reasons as string[]).join("、") : undefined,
          });
        else if (type === "review_skipped") {
          tasks.set(taskId, { ...cur, stage: undefined, review: undefined, reviewReason: undefined });
        } else if (type === "outpaint_started")
          tasks.set(taskId, { ...cur, stage: "outpainting", outpainted: true });
        return { ...s, tasks };
      });
      if (type === "task_done") {
        closeStream(taskId);
        void refreshWorkspace(sid);
        void refreshContext(sid);
        // Sticky-last-output: a single-product edit/generation becomes the new
        // focus so the next turn iterates on IT, not the original source. Only
        // for single-output image/video kinds — search/crawl produce a batch and
        // must not hijack the selection. The id comes from the task_done payload.
        const newAsset = typeof data.assetId === "string" ? data.assetId : "";
        if (newAsset && (taskKind === "generate" || taskKind === "video")) {
          setState((s) => ({ ...s, selected: new Set([newAsset]) }));
        }
        // Update the retry tool card in chat if this task was triggered by retryAsset.
        const retryCardId = retryCardsRef.current.get(taskId);
        if (retryCardId) {
          setChat((c) => c.map((it) => it.kind === "tool" && it.tool.id === retryCardId ? { ...it, tool: { ...it.tool, status: "done" } } : it));
          retryCardsRef.current.delete(taskId);
        }
      } else if (type === "task_progress" && data.asset_id) {
        // immediate backfill: each downloaded image is pushed as soon as it lands
        void refreshWorkspace(sid);
      } else if (type === "task_failed") {
        closeStream(taskId);
        toast("有一个生成任务失败了，可在工作区重试", "warn");
        const retryCardId = retryCardsRef.current.get(taskId);
        if (retryCardId) {
          setChat((c) => c.map((it) => it.kind === "tool" && it.tool.id === retryCardId ? { ...it, tool: { ...it.tool, status: "failed", error: (data.error as string) || "重试失败" } } : it));
          retryCardsRef.current.delete(taskId);
        }
      }
      // Flush a deferred follow_up once this turn's async work has fully settled:
      // the backend sent it at turn-end (when the task was merely submitted), and
      // we held it so "已完成" only appears after the artifact actually exists.
      if ((type === "task_done" || type === "task_failed") && pendingFollowUpRef.current) {
        if (!sessionHasPendingTask(stateRef.current.tasks, taskId)) {
          const fu = pendingFollowUpRef.current;
          pendingFollowUpRef.current = null;
          renderFollowUp(fu.message, fu.options);
        }
      }
    },
    [closeStream, refreshWorkspace, refreshContext, toast, renderFollowUp, setChat],
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
      for (const n of ["task_queued", "task_running", "task_progress", "task_done", "task_failed", "review_started", "review_passed", "review_failed", "review_skipped", "outpaint_started"])
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
    (sid: string, taskId: string, kind: TaskKind, note?: string, count?: number) => {
      setState((s) => {
        const existing = s.tasks.get(taskId);
        const tasks = new Map(s.tasks);
        if (existing) {
          // Backfill the note / count if newly known; otherwise leave as-is.
          const patch: Partial<Task> = {};
          if (note && !existing.note) patch.note = note;
          if (count != null && existing.count == null) patch.count = count;
          if (Object.keys(patch).length === 0) return s;
          tasks.set(taskId, { ...existing, ...patch });
        } else {
          tasks.set(taskId, { id: taskId, kind, status: "running", progress: 0, note, count });
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
  // The wait state is tiered (P0/P1/P2): on turn_start the UI shows a lightweight
  // P1 micro-hint ("正在启动深度思考…") instead of a blank or heavy static
  // loader. It escalates to the static P2 fallback only when the turn is known
  // non-streaming (backend signal) or no increment arrives within P1_TIMEOUT_MS.
  // The first model increment removes the wait bubble entirely (→ P0 typewriter).
  const P1_TIMEOUT_MS = 1500;

  const clearWaitTimer = React.useCallback(() => {
    if (waitTimerRef.current != null) {
      window.clearTimeout(waitTimerRef.current);
      waitTimerRef.current = null;
    }
  }, []);

  // escalateWait bumps the in-flight wait bubble to P2 (static fallback). No-op
  // when there is no wait bubble (the first increment already removed it).
  const escalateWait = React.useCallback(() => {
    clearWaitTimer();
    setChat((c) => {
      let changed = false;
      const next = c.map((it) => {
        if (it.kind === "loading" && it.level !== "p2") {
          changed = true;
          return { ...it, level: "p2" as const };
        }
        return it;
      });
      return changed ? next : c;
    });
  }, [clearWaitTimer, setChat]);

  // A wait bubble is shown the instant a turn starts (locally on send, and
  // reaffirmed on turn_start) so the UI never sits blank while the model spins
  // up. It enters at P1 and arms the P2 escalation timer. Removed as soon as real
  // content arrives or the turn ends. Re-invocation is idempotent (a degrade
  // turn_start arrives as a second turn_start): it never inserts a duplicate
  // bubble nor resets an already-escalated level.
  const showLoading = React.useCallback(() => {
    setState((s) => {
      if (s.chat.some((it) => it.kind === "loading")) return { ...s, thinking: true };
      return { ...s, thinking: true, chat: [...s.chat, { kind: "loading", id: uid("load"), level: "p1" }] };
    });
    clearWaitTimer();
    waitTimerRef.current = window.setTimeout(() => {
      waitTimerRef.current = null;
      escalateWait();
    }, P1_TIMEOUT_MS);
  }, [clearWaitTimer, escalateWait]);

  const clearLoading = React.useCallback(() => {
    clearWaitTimer();
    setChat((c) => c.filter((it) => it.kind !== "loading"));
  }, [clearWaitTimer, setChat]);

  // turn_reset: the backend is about to re-produce this turn from a clean slate
  // (a self-correcting retry after the model faked execution in prose). DROP the
  // in-flight assistant bubble and reasoning block entirely — unlike flushTyper /
  // collapseReasoning we do NOT keep their text, since the retry's output would
  // otherwise append to the discarded fake-ack prose and surface as duplicated
  // confirmation text (the bug this fixes). Already-landed tool cards and assets
  // are left untouched; we return to the wait state for the fresh increments.
  const onTurnReset = React.useCallback(() => {
    if (tickRef.current != null) {
      window.clearInterval(tickRef.current);
      tickRef.current = null;
    }
    const tId = typer.current.id;
    const rId = reasoner.current.id;
    typer.current = { id: "", target: "", shown: 0, done: false };
    reasoner.current = { id: "", target: "", shown: 0 };
    if (tId || rId) {
      setChat((c) => c.filter((it) => !(it.kind === "assistant" && it.id === tId) && !(it.kind === "reasoning" && it.id === rId)));
    }
    showLoading();
  }, [setChat, showLoading]);

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

  // analysisRef tracks the current in-flight analysis block (vision report). It
  // mirrors the streamed text outside React state so the confirmation countdown
  // can read the final report synchronously when it auto-submits (a setChat
  // updater value isn't readable at the call site under React 18 batching).
  const analysisRef = React.useRef<{ id: string; done: boolean; text: string }>({ id: "", done: false, text: "" });
  const onAnalysisDelta = React.useCallback((text: string, done: boolean) => {
    clearLoading();
    if (!analysisRef.current.id || analysisRef.current.done) {
      const id = uid("an");
      analysisRef.current = { id, done: false, text: "" };
      setChat((c) => [...c, { kind: "analysis", id, text: "", collapsed: false, done: false }]);
    }
    analysisRef.current.text += text || "";
    setChat((c) => c.map((it) => {
      if (it.kind !== "analysis" || it.id !== analysisRef.current.id) return it;
      const next = it.text + (text || "");
      return done ? { ...it, text: next, collapsed: true, done: true } : { ...it, text: next };
    }));
    if (done) analysisRef.current.done = true;
  }, [clearLoading, setChat]);

  // summaryTimerRef holds the active 3s confirmation countdown for an analysis
  // block: the interval handle plus the block id it ticks. Only one summary
  // confirmation can be pending at a time (adaptation is a single tool call over
  // one image group), so a single ref suffices.
  const summaryTimerRef = React.useRef<{ id: string; timer: ReturnType<typeof setInterval> | null }>({ id: "", timer: null });
  // pendingConfirmRef mirrors the in-flight confirmation's identity outside React
  // state: its block id, the cacheKey to echo back, and whether it's already been
  // submitted. The wire send (and the countdown auto-submit) read cacheKey from
  // HERE rather than from a setChat updater — under React 18 automatic batching an
  // updater's body runs deferred, so a value assigned inside it isn't readable at
  // the call site. Reading the stale "" cacheKey is exactly what made the backend
  // gate miss the deliver and hang on its 60s safety timeout before generating.
  const pendingConfirmRef = React.useRef<{ id: string; cacheKey: string; confirmed: boolean }>({ id: "", cacheKey: "", confirmed: false });
  const clearSummaryTimer = React.useCallback(() => {
    if (summaryTimerRef.current.timer) clearInterval(summaryTimerRef.current.timer);
    summaryTimerRef.current = { id: "", timer: null };
  }, []);

  // submitSummaryConfirm releases the backend adapt gate: it sends the final
  // summary (edited text or the countdown-default original) over the WS, marks
  // the block confirmed (disabling further edits), and stops the countdown. A
  // no-op once the block is already confirmed (idempotent — single confirm only).
  const submitSummaryConfirm = React.useCallback((id: string, summary: string, edited: boolean) => {
    const p = pendingConfirmRef.current;
    // Guard + cacheKey both come from the ref so they're correct synchronously,
    // regardless of React's batching of the setChat below.
    if (p.id !== id || p.confirmed) return;
    p.confirmed = true;
    const cacheKey = p.cacheKey;
    clearSummaryTimer();
    setChat((c) => c.map((it) =>
      it.kind === "analysis" && it.id === id
        ? { ...it, confirming: false, editing: false, confirmed: true }
        : it,
    ));
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: "summary_confirm", cacheKey, summary, edited }));
    }
  }, [setChat, clearSummaryTimer]);

  // onSummaryConfirm enters the editable confirmation window for the just-finished
  // analysis block: it records the cache key, starts a 3s countdown, and when the
  // countdown reaches 0 (and the user neither edited nor confirmed) auto-submits
  // the original summary as the default — so an untouched analysis flows straight
  // into adaptation with zero clicks.
  //
  // When summary is provided (re-analysis path), the new content is backfilled into
  // the original editing block instead of starting a fresh countdown on a new block.
  const onSummaryConfirm = React.useCallback((cacheKey: string, summary?: string) => {
    const id = analysisRef.current.id;
    if (!id || !analysisRef.current.done) return;
    clearSummaryTimer();

    if (summary != null) {
      // Re-analysis: remove the transient streaming block, update the original
      // reanalyzing block with new text, and keep it in edit mode (no countdown).
      let origId = "";
      setChat((c) => c
        .filter((it) => !(it.kind === "analysis" && it.id === id))
        .map((it) => {
          if (it.kind !== "analysis" || !it.reanalyzing) return it;
          origId = it.id;
          return { ...it, text: summary, cacheKey, editing: true, reanalyzing: false };
        }),
      );
      if (origId) {
        analysisRef.current = { id: origId, done: true, text: summary };
        pendingConfirmRef.current = { id: origId, cacheKey, confirmed: false };
      }
      return;
    }

    pendingConfirmRef.current = { id, cacheKey, confirmed: false };
    setChat((c) => c.map((it) => {
      if (it.kind !== "analysis") return it;
      // Clear reanalyzing loading state on any prior block before re-arming.
      if (it.id !== id) return it.reanalyzing ? { ...it, reanalyzing: false } : it;
      return { ...it, cacheKey, confirming: true, secondsLeft: 3, editing: false, confirmed: false, reanalyzing: false };
    }));
    // Drive the countdown off a local counter (not the setChat updater's return),
    // so the expiry decision is synchronous and independent of batching. setChat
    // here only reflects the remaining seconds in the UI.
    let left = 3;
    const timer = setInterval(() => {
      left -= 1;
      setChat((c) => c.map((it) =>
        it.kind === "analysis" && it.id === id && !it.editing && !it.confirmed
          ? { ...it, secondsLeft: Math.max(0, left) }
          : it,
      ));
      if (left <= 0) submitSummaryConfirm(id, analysisRef.current.text, false);
    }, 1000);
    summaryTimerRef.current = { id, timer };
  }, [clearSummaryTimer, setChat, submitSummaryConfirm]);

  // editSummary pauses the countdown, puts the block into edit mode, and
  // notifies the backend to cancel its safety timeout so an edit taking longer
  // than the original 8s backstop is still delivered and written back.
  const editSummary = React.useCallback((id: string) => {
    clearSummaryTimer();
    setChat((c) => c.map((it) =>
      it.kind === "analysis" && it.id === id ? { ...it, editing: true } : it,
    ));
    const ws = wsRef.current;
    const cacheKey = pendingConfirmRef.current.cacheKey;
    if (ws && ws.readyState === WebSocket.OPEN && cacheKey) {
      ws.send(JSON.stringify({ type: "summary_editing", cacheKey }));
    }
  }, [clearSummaryTimer, setChat]);

  // reanalyzeSummary sends a fresh-analysis request to the backend, which will
  // re-run grok on the same reference group and stream the new report back.
  // The current block enters "reanalyzing" loading state (submit disabled).
  const reanalyzeSummary = React.useCallback((id: string) => {
    setChat((c) => c.map((it) =>
      it.kind === "analysis" && it.id === id ? { ...it, reanalyzing: true } : it,
    ));
    const ws = wsRef.current;
    const cacheKey = pendingConfirmRef.current.cacheKey;
    if (ws && ws.readyState === WebSocket.OPEN && cacheKey) {
      ws.send(JSON.stringify({ type: "summary_reanalyze", cacheKey }));
    }
  }, [setChat]);

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
    // Reset the adapt task collector when a new adapt call starts.
    if (card.name === "adapt_to_platform") pendingAdaptRef.current = [];
    // Reset + capture dimension when a new generate_variants call starts, so the
    // per-variant task_created events collected below land in a fresh batch.
    if (card.name === "generate_variants") {
      const dim = args && typeof args.dimension === "string" ? args.dimension : "style";
      pendingVariantsRef.current = { taskIds: [], dimension: dim };
    }
    setChat((c) => [...c, { kind: "tool", id: card.id!, tool: card }]);
  }, [flushTyper, collapseReasoning, setChat]);

  const onToolResult = React.useCallback((sid: string, data: Record<string, unknown>) => {
    const ok = data.status !== "error";
    const name = data.name as string | undefined;
    if (ok && typeof data.task_id === "string") {
      ensureTaskPlaceholder(sid, data.task_id, ((data.kind as TaskKind) || "generate"), lastToolNoteRef.current);
      lastToolNoteRef.current = undefined;
    } else if (ok && name && SYNC_ASSET_TOOLS.has(name)) {
      void refreshWorkspace(sid);
    }
    // Insert an adapt pipeline timeline after the tool card for adapt_to_platform.
    if (ok && name === "adapt_to_platform") {
      const ids = [...pendingAdaptRef.current];
      if (ids.length > 0) {
        const panelId = uid("ap");
        setChat((c) => [...c, { kind: "adapt_pipeline" as const, id: panelId, taskIds: ids }]);
        // Stash so a refresh mid-adaptation can rebuild this panel (Bug: panel
        // vanished on reload while its tasks kept running).
        saveAdaptPanels(sid, [...loadAdaptPanels(sid), { id: panelId, taskIds: ids }]);
      }
    }
    // generate_copy is synchronous: its structured copy rides in the tool_result
    // data. Render it as a dedicated copy card so the user gets a grouped,
    // copy-able view (title / slogans / selling points / platform copy).
    if (ok && name === "generate_copy") {
      const slogans = Array.isArray(data.slogans) ? (data.slogans as string[]) : undefined;
      const sellingPoints = Array.isArray(data.selling_points) ? (data.selling_points as string[]) : undefined;
      const title = typeof data.title === "string" ? data.title : undefined;
      const platformCopy = typeof data.platform_copy === "string" ? data.platform_copy : undefined;
      if (title || slogans?.length || sellingPoints?.length || platformCopy) {
        setChat((c) => [...c, { kind: "copy" as const, id: uid("copy"), title, slogans, sellingPoints, platformCopy }]);
      }
    }
    // generate_variants is an async ReturnDirectly tool: its friendly ack is the
    // model reply, so the structured task_ids do NOT ride the tool_result event.
    // We instead use the per-variant task_created events collected in
    // pendingVariantsRef (mirroring adapt) and render one grouped, comparable
    // cluster — each variant's product fills the workspace via its own task SSE.
    if (ok && name === "generate_variants") {
      const { taskIds, dimension: dimKey } = pendingVariantsRef.current;
      pendingVariantsRef.current = { taskIds: [], dimension: "" };
      if (taskIds.length > 0) {
        const dimension = variantDimensionLabel(dimKey);
        const labels = taskIds.map((_, i) => `${dimension}变体 ${i + 1}`);
        setChat((c) => [...c, { kind: "variants_group" as const, id: uid("vg"), batchId: uid("batch"), dimension, taskIds: [...taskIds], labels }]);
      }
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
  }, [ensureTaskPlaceholder, setChat, refreshWorkspace]);

  const finishPendingTools = React.useCallback(() => {
    setChat((c) => c.map((it) => (it.kind === "tool" && it.tool.status === "running" ? { ...it, tool: { ...it.tool, status: "done" } } : it)));
  }, [setChat]);

  // sendNow performs the actual WS send for one user message: it appends the
  // user bubble, shows the loading state, and emits the user_message frame with
  // the current asset display order. Callers gate on `thinking` before calling.
  const sendNow = React.useCallback((value: string, ref?: string | string[], sizeIds?: string[]) => {
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
    // Hidden platform-adaptation size ids: sent to the agent, not shown in the
    // bubble (the displayed text already names the sizes in human terms).
    if (sizeIds && sizeIds.length) payload.sizeIds = sizeIds;
    ws.send(JSON.stringify(payload));
    // Sticky-last-output: consume the explicit selection now that it is captured
    // in the payload. The next turn defaults to the produced output (auto-selected
    // on task_done) or, absent a new product, the backend's [上次产物] anchor —
    // so a stale selection never silently overrides the latest image.
    if (stateRef.current.selected.size > 0) {
      setState((s) => (s.selected.size > 0 ? { ...s, selected: new Set() } : s));
    }
  }, [setChat, toast, showLoading]);

  const onTurnEnd = React.useCallback((sid: string, data: Record<string, unknown>) => {
    clearLoading();
    setState((s) => ({ ...s, thinking: false }));
    flushTyper();
    finishPendingTools();
    // Guard: if summary_confirm was never sent (e.g. backend skipped the gate on
    // retry), reanalyzing would stay true forever — clear it on turn end.
    setChat((c) => c.map((it) => (it.kind === "analysis" && it.reanalyzing ? { ...it, reanalyzing: false } : it)));
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
      setTimeout(() => sendNow(next.text, next.ref, next.sizeIds), 0);
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
          // A degrade arrives as a second turn_start carrying streaming:false;
          // treat the repeat idempotently — do NOT reset producedRef, just show
          // the (existing) wait bubble and escalate to the static P2 fallback.
          if (d.streaming === false) {
            showLoading();
            escalateWait();
          } else {
            producedRef.current = false;
            // A new turn supersedes any follow_up still waiting on the prior turn's
            // tasks — drop it so it can't later attach to this turn's work.
            pendingFollowUpRef.current = null;
            showLoading();
          }
          break;
        case "turn_end":
          onTurnEnd(sid, d);
          break;
        case "turn_reset":
          // Backend discarded this turn's faked-ack increments before a retry;
          // drop the in-flight bubble so the retry output doesn't duplicate it.
          onTurnReset();
          break;
        case "capsule":
          producedRef.current = true;
          onCapsule(d);
          break;
        case "message":
          if ((d.text as string) || "") producedRef.current = true;
          if (d.analysis) {
            onAnalysisDelta((d.text as string) || "", !!d.done);
          } else {
            onAssistantDelta((d.text as string) || "", !!d.done);
            if (d.done) { finishPendingTools(); void refreshContext(sid); }
          }
          break;
        case "reasoning":
          onReasoning((d.text as string) || "");
          break;
        case "summary_confirm":
          // Live analysis finished: open the editable 3s confirmation window on
          // the just-completed analysis block. Cache hits never emit this, so the
          // window only appears when the user can actually steer a fresh report.
          onSummaryConfirm((d.cacheKey as string) || "", d.summary as string | undefined);
          break;
        case "tool_call":
          producedRef.current = true;
          onToolCall(d);
          break;
        case "tool_result":
          onToolResult(sid, d);
          break;
        case "follow_up": {
          const msg = (d.message as string) || "接下来想做什么？";
          const rawOpts = Array.isArray(d.options) ? (d.options as Record<string, unknown>[]) : [];
          const opts = rawOpts.map((o) => ({
            label: (o.label as string) || "",
            value: (o.value as string) || "",
          }));
          // Defer until async tasks settle: the backend emits follow_up at turn-end,
          // but generate/video/search turns end while the task is still running, so
          // showing "已完成" now would precede the actual artifact. Stash it and let
          // applyTaskEvent flush it once no task is queued/running. When nothing is
          // pending (synchronous tools like crop, or already finished), render now.
          if (sessionHasPendingTask(stateRef.current.tasks)) {
            pendingFollowUpRef.current = { message: msg, options: opts };
          } else {
            renderFollowUp(msg, opts);
          }
          break;
        }
        case "task_created":
          if (typeof d.task_id === "string") {
            ensureTaskPlaceholder(
              sid,
              d.task_id,
              (d.kind as TaskKind) || "generate",
              undefined,
              typeof d.count === "number" ? d.count : undefined,
            );
            // Collect task IDs created during an adapt / variants call for their
            // grouped chat items (consumed in onToolResult). Both are generate-kind
            // tasks; the matching tool reads only its own ref, so pushing to both is
            // harmless (one call type is active per turn).
            if ((d.kind as TaskKind) === "generate") {
              pendingAdaptRef.current.push(d.task_id);
              pendingVariantsRef.current.taskIds.push(d.task_id);
            }
          }
          break;
        case "error":
          clearLoading();
          setState((s) => ({ ...s, thinking: false }));
          toast((d.message as string) || "发生未知错误");
          break;
      }
    };
  }, [onAssistantDelta, onReasoning, onToolCall, onToolResult, ensureTaskPlaceholder, finishPendingTools, refreshContext, toast, showLoading, escalateWait, onTurnEnd, onCapsule, clearLoading, onTurnReset, renderFollowUp, onAnalysisDelta, onSummaryConfirm]);

  // ============ actions ============
  // sendMessage routes a user input: when a turn is in flight it joins the
  // pending queue (auto-flushed on turn_end); otherwise it sends immediately.
  const sendMessage = React.useCallback((text: string, ref?: string | string[], sizeIds?: string[]) => {
    const value = text.trim();
    if (!value) return;
    if (stateRef.current.thinking) {
      setState((s) => ({ ...s, queue: [...s.queue, { id: uid("q"), text: value, ref, sizeIds }] }));
      return;
    }
    sendNow(value, ref, sizeIds);
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
        const bootTasks = await refreshWorkspace(sid);
        // Rebuild any in-flight "适配流程" panels stashed before the reload. Keep
        // only panels whose tasks still exist and have at least one unfinished
        // (queued/running) task; rewrite the stash to the surviving set so done
        // batches don't accumulate. Use the freshly-fetched bootTasks (not stateRef,
        // which may not have re-rendered yet after refreshWorkspace's setState).
        if (alive) {
          const byId = new Map(bootTasks.map((t) => [t.id, t] as const));
          const survivors: StashedAdaptPanel[] = [];
          for (const p of loadAdaptPanels(sid)) {
            const live = p.taskIds.filter((id) => byId.has(id));
            const unfinished = live.some((id) => {
              const t = byId.get(id);
              return t && t.status !== "done" && t.status !== "failed";
            });
            if (live.length && unfinished) survivors.push({ id: p.id, taskIds: live });
          }
          saveAdaptPanels(sid, survivors);
          if (survivors.length) {
            setChat((c) => [
              ...c,
              ...survivors.map((p) => ({ kind: "adapt_pipeline" as const, id: p.id, taskIds: p.taskIds })),
            ]);
          }
        }
        // Restore the reference selection persisted before the reload, filtered to
        // assets that still exist (a deleted/cleared asset must not resurrect). The
        // chat "已选 N 张" count is purely client-side, so without this a refresh
        // would silently drop the user's pick. restoredRef gates the persist effect
        // so it only starts mirroring AFTER this read (else the initial empty render
        // overwrites the stored value first).
        if (alive) {
          try {
            const raw = sessionStorage.getItem(SELECTED_KEY);
            const ids: string[] = raw ? JSON.parse(raw) : [];
            if (ids.length) {
              setState((s) => {
                const sel = new Set(ids.filter((id) => s.assets.has(id)));
                return sel.size ? { ...s, selected: sel } : s;
              });
            }
          } catch { /* ignore malformed persisted selection */ }
          restoredRef.current = true;
        }
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
      if (waitTimerRef.current != null) window.clearTimeout(waitTimerRef.current);
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // ============ workspace actions ============
  const toggleSelect = React.useCallback((id: string) => {
    setState((s) => {
      const sel = new Set(s.selected);
      if (sel.has(id)) {
        sel.delete(id);
      } else {
        sel.add(id);
      }
      return { ...s, selected: sel };
    });
  }, []);

  const selectAll = React.useCallback(() => {
    setState((s) => {
      const ids = orderedAssetIds(s).filter((id) => s.assets.has(id));
      return { ...s, selected: new Set(ids) };
    });
  }, []);

  const clearSelection = React.useCallback(() => {
    setState((s) => ({ ...s, selected: new Set() }));
  }, []);

  const setLossless = React.useCallback((v: boolean) => {
    setState((s) => ({ ...s, lossless: v }));
  }, []);

  // loadModels fetches the per-scene catalog + current selection for the picker.
  const loadModels = React.useCallback(async () => {
    const sid = stateRef.current.sessionId;
    if (!sid) return;
    try {
      const m = await api.getModels(sid);
      setState((s) => ({ ...s, models: { catalog: m.catalog || {}, selected: m.selected || {}, defaults: m.defaults || {} } }));
    } catch (e) {
      toast("加载模型列表失败：" + (e as Error).message);
    }
  }, [toast]);

  // switchModel sets the session's model for a scene; the chat scene triggers a
  // self-introduction that streams in over the normal chat channel. The selection
  // is updated optimistically.
  const switchModel = React.useCallback(async (scene: string, model: string) => {
    const sid = stateRef.current.sessionId;
    if (!sid) return;
    try {
      await api.switchModel(sid, scene, model);
      setState((s) => ({
        ...s,
        models: s.models ? { ...s.models, selected: { ...s.models.selected, [scene]: model } } : s.models,
      }));
    } catch (e) {
      toast("切换模型失败：" + (e as Error).message);
    }
  }, [toast]);

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
      // toast("已移除", "ok");
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

  // retryAsset re-runs the AI flow behind a SUCCEEDED product. The backend yields
  // a NEW task (original asset untouched); we seed a placeholder for it so the new
  // product streams into the workspace just like a fresh generation.
  const retryAsset = React.useCallback(async (assetId: string) => {
    const sid = stateRef.current.sessionId;
    const cardId = uid("tc");
    setChat((c) => [...c, { kind: "tool", id: cardId, tool: { id: cardId, name: "edit_image", args: { intent: "retry" }, status: "running" } }]);
    try {
      const { taskId } = await api.retryAsset(sid, assetId);
      if (taskId) {
        retryCardsRef.current.set(taskId, cardId);
        ensureTaskPlaceholder(sid, taskId, "generate", "重试生成");
      }
    } catch (e) {
      setChat((c) => c.map((it) => it.kind === "tool" && it.tool.id === cardId ? { ...it, tool: { ...it.tool, status: "failed", error: (e as Error).message } } : it));
      toast("重试失败：" + (e as Error).message);
    }
  }, [ensureTaskPlaceholder, setChat, toast]);

  const clearWorkspace = React.useCallback(async () => {
    const sid = stateRef.current.sessionId;
    try {
      await api.clearWorkspace(sid);
      // Clear assets/tasks locally too. refreshWorkspace MERGES (spreads
      // existing) to survive concurrent refreshes, so it would never drop the
      // now-deleted assets on its own — leaving state.assets non-empty and
      // trapping the Shell out of its empty/home state. Reset here first.
      setState((s) => ({ ...s, selected: new Set(), chat: [], assets: new Map(), tasks: new Map() }));
      saveAdaptPanels(sid, []); // tasks are gone — drop any stashed adapt panels
      typer.current = { id: "", target: "", shown: 0, done: false };
      reasoner.current = { id: "", target: "", shown: 0 };
      await refreshWorkspace(sid);
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
    const newIds = results.flatMap((r) => (r.status === "fulfilled" ? [r.value.id] : []));
    setState((s) => {
      const assets = new Map(s.assets);
      for (const r of results) if (r.status === "fulfilled") assets.set(r.value.id, r.value);
      const selected = new Set(newIds);
      return { ...s, assets, selected };
    });
    const ok = results.filter((r) => r.status === "fulfilled").length;
    const fail = results.length - ok;
    if (fail > 0) {
      if (ok === 0) toast(`上传失败 ${fail} 张`, "error");
      else toast(`已上传 ${ok} 张，失败 ${fail} 张`, "warn");
    }
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
    loadModels,
    switchModel,
    removeAsset,
    removeSelected,
    removeTask,
    clearFailed,
    retryTask,
    retryAsset,
    clearWorkspace,
    clearContext,
    uploadFiles,
    toast,
    collapseReasoningItem: (id: string) =>
      setChat((c) => c.map((it) => (it.kind === "reasoning" && it.id === id ? { ...it, collapsed: !it.collapsed } : it))),
    collapseAnalysisItem: (id: string) =>
      setChat((c) => c.map((it) => (it.kind === "analysis" && it.id === id ? { ...it, collapsed: !it.collapsed } : it))),
    submitSummaryConfirm,
    editSummary,
    reanalyzeSummary,
    dismissFollowUp: (id: string) =>
      setChat((c) => c.map((it) => (it.kind === "follow_up" && it.id === id ? { ...it, dismissed: true } : it))),
  } as const;
}
