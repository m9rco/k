import type { Asset, Task } from "./types";

// A TimelineNode is one creation event in the workshop's production line:
// an upload, a generate/edit, a crop (one→many), a video, or a crawl. Nodes are
// synthesized purely on the frontend from tasks + assets; nothing is persisted.
export interface TimelineNode {
  // Stable key for keyed rendering. A task's id is preferred (so a running task
  // keeps its key when it turns into a product in place); otherwise the (first)
  // asset id.
  key: string;
  // Sort time in ms. Assets use createdAt; active/failed tasks float to the
  // active end (now) so they stay visible at the top.
  at: number;
  state: "running" | "failed" | "done";
  kind: "generate" | "video" | "crop" | "crawl" | "upload" | "search";
  // Produced assets (empty while running/failed; crop/crawl may hold several).
  assets: Asset[];
  // The originating task, present while running/failed (progress, retry).
  task?: Task;
  // Derived-from source asset id (from a product's parentId), when identifiable.
  parentId?: string;
}

// kindFromTask maps a task kind to a node kind.
function kindFromTask(k: Task["kind"]): TimelineNode["kind"] {
  if (k === "video") return "video";
  if (k === "crawl") return "crawl";
  if (k === "search") return "search";
  return "generate";
}

// kindFromAsset maps an asset kind to a node kind.
function kindFromAsset(k: Asset["kind"]): TimelineNode["kind"] {
  switch (k) {
    case "upload": return "upload";
    case "video": return "video";
    case "cropped": return "crop";
    case "crawled": return "crawl";
    case "searched": return "search";
    default: return "generate";
  }
}

function ts(s?: string): number {
  if (!s) return 0;
  const n = Date.parse(s);
  return Number.isNaN(n) ? 0 : n;
}

// buildTimeline synthesizes the ordered node list (newest first) from the
// current tasks and assets. nowMs is injected for deterministic tests.
//
// Aggregation: a single operation that yields multiple products (crop sizes,
// crawl results) is collapsed into one node, keyed by source(parentId)+kind and
// a same-second time window. When that can't be determined the products fall
// back to one node each (still time-ordered) — a known tradeoff until the
// backend supplies a batch id.
export function buildTimeline(
  assets: Asset[],
  tasks: Task[],
  nowMs: number,
): TimelineNode[] {
  // assetId -> producing task (so a finished task's node keeps the task key).
  const taskByAsset = new Map<string, Task>();
  for (const t of tasks) if (t.assetId) taskByAsset.set(t.assetId, t);
  // id -> task, used to resolve a searched asset's batch task from its parentId
  // (search assets carry parentId = their search task id, not a source asset).
  const taskById = new Map<string, Task>();
  for (const t of tasks) taskById.set(t.id, t);

  // 1) Group assets into product nodes.
  const groups = new Map<string, TimelineNode>();
  const order: string[] = []; // preserve first-seen order of group keys
  // search task ids that already have a product node, so step 2 does not also
  // emit a separate running placeholder node for them (would duplicate the key).
  const groupedSearchTaskIds = new Set<string>();
  for (const a of assets) {
    const nodeKind = kindFromAsset(a.kind);
    // A searched asset belongs to its search batch task (parentId = task id);
    // the whole batch collapses into one 搜图 node regardless of download timing.
    const searchTask = nodeKind === "search" ? taskById.get(a.parentId || "") : undefined;
    // Aggregate multi-product ops (crop/crawl/platform-adaptation) by source +
    // kind + same-second. Platform-adaptation AI products have kind="generated"
    // but carry sizeId, so they behave like crop for grouping purposes.
    const aggregatable = nodeKind === "crop" || nodeKind === "crawl" || (nodeKind === "generate" && !!a.sizeId);
    const aggKind = aggregatable && nodeKind === "generate" ? "crop" : nodeKind;
    const sourceTask = taskByAsset.get(a.id);
    let groupKey: string;
    if (searchTask) {
      groupKey = `task:${searchTask.id}`;
    } else if (aggregatable) {
      // Platform-adaptation products (sizeId set) all derive from one source in
      // one batch — no time bucket needed. Regular crop/crawl use same-second
      // bucketing so two separate crops of the same image stay distinct.
      const bucket = a.sizeId ? "" : `:${Math.floor(ts(a.createdAt) / 1000)}`;
      groupKey = `agg:${aggKind}:${a.parentId || "_"}${bucket}`;
    } else if (sourceTask) {
      groupKey = `task:${sourceTask.id}`;
    } else {
      groupKey = `asset:${a.id}`;
    }
    const groupTask = searchTask || sourceTask;
    const existing = groups.get(groupKey);
    if (existing) {
      existing.assets.push(a);
      existing.at = Math.max(existing.at, ts(a.createdAt));
    } else {
      if (searchTask) groupedSearchTaskIds.add(searchTask.id);
      groups.set(groupKey, {
        key: groupTask ? `task:${groupTask.id}` : `asset:${a.id}`,
        at: ts(a.createdAt),
        state: "done",
        kind: nodeKind,
        assets: [a],
        // Carry the producing task so its note (the agent's understanding of the
        // operation) is available on the finished product node.
        task: groupTask,
        // A search asset's parentId is its task id (not a derivation source), so
        // it must not surface a "由 图N 加工" label.
        parentId: nodeKind === "search" ? undefined : a.parentId,
      });
      order.push(groupKey);
    }
  }
  // Refine search batch nodes: while the task is still running, present the node
  // at the active end with running state so it shows remaining placeholder slots
  // alongside the images already downloaded.
  for (const key of order) {
    const n = groups.get(key)!;
    if (n.kind !== "search" || !n.task) continue;
    if (n.task.status === "failed") n.state = "failed";
    else if (n.task.status !== "done") {
      n.state = "running";
      n.at = nowMs;
    }
  }

  // 2) Active / failed tasks that have NOT yet produced an asset become nodes
  // at the active end. (A task whose asset already arrived is represented by the
  // product node above, keyed by task id.)
  const producedTaskIds = new Set<string>();
  for (const t of tasks) if (t.assetId) producedTaskIds.add(t.id);
  const taskNodes: TimelineNode[] = [];
  for (const t of tasks) {
    if (t.status === "done") continue; // its product node covers it
    if (producedTaskIds.has(t.id)) continue;
    if (groupedSearchTaskIds.has(t.id)) continue; // already a search batch node
    taskNodes.push({
      key: `task:${t.id}`,
      at: nowMs, // float to the active end
      state: t.status === "failed" ? "failed" : "running",
      kind: kindFromTask(t.kind),
      assets: [],
      task: t,
    });
  }

  const nodes = [...order.map((k) => groups.get(k)!), ...taskNodes];
  // Newest first; ties broken by key for stability.
  nodes.sort((a, b) => (b.at - a.at) || (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
  return nodes;
}

// AssetLabels maps an asset id to its display label ("图N" / "视频N"). Numbering
// is the LLM-communication anchor: images and videos are counted separately, in
// creation order (earliest = 1), independent of timeline display direction.
export function assetLabels(assets: Asset[]): Map<string, string> {
  const byTime = [...assets].sort((a, b) => ts(a.createdAt) - ts(b.createdAt));
  const labels = new Map<string, string>();
  let img = 0;
  let vid = 0;
  for (const a of byTime) {
    const isVideo = a.kind === "video" || (a.mime || "").startsWith("video/");
    if (isVideo) {
      vid += 1;
      labels.set(a.id, `视频${vid}`);
    } else {
      img += 1;
      labels.set(a.id, `图${img}`);
    }
  }
  return labels;
}

// relativeTime formats a node's creation time as a short Chinese relative label
// ("刚刚 / N 分钟前 / N 小时前 / HH:mm"). Returns "" for unknown time.
export function relativeTime(createdAt: string | undefined, nowMs: number): string {
  const t = ts(createdAt);
  if (!t) return "";
  const diff = Math.max(0, nowMs - t);
  const min = Math.floor(diff / 60000);
  if (min < 1) return "刚刚";
  if (min < 60) return `${min} 分钟前`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} 小时前`;
  const d = new Date(t);
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${hh}:${mm}`;
}

// describeToolCall turns a tool-call's name + args into a short Chinese summary
// of what the agent understood the operation to be — shown on the timeline node
// so the user sees "the LLM's read" of each step. Falls back to the action name.
export function describeToolCall(name: string, args: Record<string, unknown> | undefined): string {
  const a = args || {};
  const s = (k: string): string => (typeof a[k] === "string" ? (a[k] as string) : "");
  switch (name) {
    case "edit_image": {
      const intent = s("intent");
      const verb = intent === "change_character" ? "换角色"
        : intent === "change_background" ? "换背景"
        : intent === "change_text" ? "换文案"
        : "编辑图片";
      const detail = s("background_desc") || s("character_desc") || s("text_content");
      return detail ? `${verb} · ${detail}` : verb;
    }
    case "image_to_video": {
      const motion = s("motion");
      return motion ? `生视频 · ${motion}` : "生视频";
    }
    case "crop_to_sizes": {
      const ids = Array.isArray(a.size_ids) ? (a.size_ids as unknown[]).length : 0;
      return ids ? `切尺寸 · ${ids} 个规格` : "切尺寸";
    }
    case "crawl_game_assets": {
      const game = s("game");
      return game ? `爬取素材 · ${game}` : "爬取素材";
    }
    default:
      return name;
  }
}

