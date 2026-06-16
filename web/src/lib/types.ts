// Shared types mirroring the Go backend's JSON shapes. Kept minimal — only the
// fields the UI consumes.

export type AssetKind = "upload" | "generated" | "cropped" | "crawled" | "searched" | "video";

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

// ModelEntry is one selectable model in the per-session model catalog.
export interface ModelEntry {
  id: string;
  displayName: string;
  scene: string;
  vendor: string;
  iconKey: string;
}
