// Shared types mirroring the Go backend's JSON shapes. Kept minimal — only the
// fields the UI consumes.

export type AssetKind = "upload" | "generated" | "cropped" | "crawled" | "video";

export interface Asset {
  id: string;
  kind: AssetKind;
  url: string;
  mime?: string;
  width?: number;
  height?: number;
  parentId?: string;
}

export type TaskStatus = "queued" | "running" | "done" | "failed";
export type TaskKind = "generate" | "video" | "crawl";

export interface Task {
  id: string;
  kind: TaskKind;
  status: TaskStatus;
  progress: number;
  error?: string;
  assetId?: string;
}

export interface ContextState {
  estimatedTokens: number;
  budget: number;
  compressed: boolean;
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
