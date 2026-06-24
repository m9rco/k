// Shared types mirroring the Go backend's JSON shapes. Kept minimal — only the
// fields the UI consumes.

export type AssetKind = "upload" | "generated" | "cropped" | "crawled" | "searched" | "video" | "composite";

export interface Asset {
  id: string;
  kind: AssetKind;
  url: string;
  mime?: string;
  width?: number;
  height?: number;
  parentId?: string;
  // sizeId is set for platform-adaptation products (crop fast path or AI repaint).
  // The timeline uses it to collapse a batch of adapted sizes into one node.
  sizeId?: string;
  // retryable is true for AI products carrying a generation origin (re-runnable).
  // Uploads and deterministic crops are false → no retry affordance in the UI.
  retryable?: boolean;
  // referenceIds lists the reference asset ids used to produce this asset (≥2 means multi-ref).
  referenceIds?: string[];
  createdAt?: string;
}

export type TaskStatus = "queued" | "running" | "done" | "failed";
export type TaskKind = "generate" | "video" | "crawl" | "search";
// ReviewState tracks the platform-adaptation quality gate's sub-state on a
// generate task's placeholder card: "checking" while the judge scores, "passed"
// (✓) when it clears, "failed" (✗, then regenerating with hints). Absent when
// the task has no quality gate or the gate was skipped/degraded.
export type ReviewState = "checking" | "passed" | "failed";

export interface Task {
  id: string;
  kind: TaskKind;
  status: TaskStatus;
  progress: number;
  error?: string;
  assetId?: string;
  // count is how many product slots this task will yield (1 for single-output
  // tasks; N for a search batch downloading N images). Drives the number of
  // placeholder slots shown while the task runs. Client-only (from task_created),
  // preserved across workspace refreshes since the /tasks API does not return it.
  count?: number;
  // note is a short human-readable summary of the agent's understanding of this
  // operation (derived from the tool-call args), shown on the timeline node —
  // e.g. "换背景 · 淡紫色渐变".
  note?: string;
  // review is the quality-gate sub-state (platform adaptation only). Driven by
  // review_started/passed/failed events on this task's SSE stream; the card shows
  // a lightweight 审核中 / ✓ / ✗按建议重绘中 marker without exposing scores.
  review?: ReviewState;
  // reviewReason is a short failure cause (red line / low dimension) for an
  // optional tooltip; the card does not surface raw scores.
  reviewReason?: string;
  // stage tracks which pipeline step is currently active (adapt tasks only):
  // undefined=生图中, "outpainting"=补全中, "reviewing"=质量审核中.
  stage?: "outpainting" | "reviewing";
  // outpainted is set true once an outpaint_started event arrives, so the pipeline
  // can distinguish "补全完成" from "补全跳过" (not every size needs outpaint — only
  // extreme-ratio reshapes; near-ratio sizes converge by plain scale).
  outpainted?: boolean;
  // sizeId binds an adapt task to its platform-size slot. Client-only: derived
  // from the task_queued event (the /tasks API does not return it) so the stamp
  // album can map a task_failed back to the slot and offer an in-place retry.
  sizeId?: string;
}

export interface ContextState {
  estimatedTokens: number;
  budget: number;
  compressed: boolean;
  systemTokens?: number; // base cost of system prompt; subtracted for net display
}

// Real-time event envelope (WS + SSE share this shape).
export interface WireEvent {
  type: string;
  seq?: number;
  sessionId?: string;
  taskId?: string;
  data?: Record<string, unknown>;
  at?: string;
}

// Platform crop catalog.
export interface SizePreset {
  id: string;
  name: string;
  width: number;
  height: number;
  orientation: string;
  format?: string;
  maxKB?: number;
  note?: string;
  producible: boolean;
}

export interface AssetTypeGroup {
  type: string;
  name: string;
  sizes: SizePreset[];
}

export interface Channel {
  id: string;
  name: string;
  group: string;
  assetTypes: AssetTypeGroup[];
}

// Chat log items.
export type ChatRole = "user" | "assistant" | "system";

export interface ToolCardData {
  id?: string;
  name: string;
  args?: Record<string, unknown>;
  status: "running" | "done" | "failed";
  summary?: string;
  error?: string;
}

// AdaptPipelineItem appears in the chat log for adapt_to_platform operations,
// showing a live 4-step horizontal pipeline timeline (分析→生图→补全→审核)
// driven by task SSE events. taskIds are the adapt task(s) for this operation.
export interface AdaptPipelineItem {
  kind: "adapt_pipeline";
  id: string;
  taskIds: string[];
}

// VariantsGroupItem appears in the chat log for generate_variants operations,
// showing a batch of N creative variants as one group so the buyer can compare
// them side by side. Each variant is an independent generate task; the group
// reads each task's live status from state.tasks. labels[i] pairs with
// taskIds[i] (e.g. "风格变体 1"). dimension is the variant axis (风格/配色/…).
export interface VariantsGroupItem {
  kind: "variants_group";
  id: string;
  batchId: string;
  dimension: string;
  taskIds: string[];
  labels: string[];
}

// PlanStepStatus tracks one step of a submit_plan execution as plan_* events
// arrive. pending → running → done, or failed; steps after a failure stay
// "skipped" (the executor aborts the whole plan on first failure).
export type PlanStepStatus = "pending" | "running" | "done" | "failed" | "skipped";

// PlanItem appears in the chat log for a submit_plan multi-step orchestration.
// It renders an ordered checklist that lights up step by step as the server
// drives the plan serially. status is the overall plan state; reason carries the
// failure message for the failed step (if any).
export interface PlanItem {
  kind: "plan";
  id: string; // chat item id
  planId: string; // server plan id (matches plan_* events)
  status: "running" | "completed" | "aborted";
  steps: { id: string; tool: string; title: string; status: PlanStepStatus; reason?: string }[];
}

// ModelEntry is one selectable model in the per-session model catalog.
export interface ModelEntry {
  id: string;
  displayName: string;
  scene: string;
  vendor: string;
  iconKey: string;
}
