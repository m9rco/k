import type { Asset, Task, ToolCardData } from "@/lib/types";

// Chat log is an ordered list of items: user/assistant bubbles, reasoning
// blocks, and tool cards. Each carries a stable id for keyed rendering.
export type ChatItem =
  | { kind: "user"; id: string; text: string }
  | { kind: "assistant"; id: string; text: string; streaming: boolean }
  | { kind: "reasoning"; id: string; text: string; collapsed: boolean; done: boolean }
  | { kind: "tool"; id: string; tool: ToolCardData };

export interface ConnState {
  connected: boolean;
}

export interface AppState {
  sessionId: string;
  connected: boolean;
  chat: ChatItem[];
  assets: Map<string, Asset>;
  tasks: Map<string, Task>;
  order: string[]; // display order of asset ids
  selected: Set<string>;
  lossless: boolean;
  context: { estimatedTokens: number; budget: number; compressed: boolean } | null;
}

export const initialState: AppState = {
  sessionId: "",
  connected: false,
  chat: [],
  assets: new Map(),
  tasks: new Map(),
  order: [],
  selected: new Set(),
  lossless: true,
  context: null,
};

let seq = 0;
export const uid = (p = "i") => `${p}_${Date.now().toString(36)}_${(seq++).toString(36)}`;
