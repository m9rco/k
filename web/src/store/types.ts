import type { Asset, Task, ToolCardData } from "@/lib/types";

// Chat log is an ordered list of items: user/assistant bubbles, reasoning
// blocks, and tool cards. Each carries a stable id for keyed rendering.
export type ChatItem =
  | { kind: "user"; id: string; text: string }
  | { kind: "assistant"; id: string; text: string; streaming: boolean }
  | { kind: "reasoning"; id: string; text: string; collapsed: boolean; done: boolean }
  | { kind: "tool"; id: string; tool: ToolCardData }
  | { kind: "capsule"; id: string; question: string; options: CapsuleOption[]; answered: boolean }
  | { kind: "loading"; id: string };

// CapsuleOption is one choice in a clarify prompt. value is sent on a plain
// click; editableHint pre-fills the inline editor so the user can refine it
// before submitting.
export interface CapsuleOption {
  label: string;
  value: string;
  editableHint?: string;
}

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
  // thinking is true between turn_start and turn_end: the agent has acknowledged
  // the message and is working, even before the first model increment arrives.
  thinking: boolean;
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
  thinking: false,
  context: null,
};

let seq = 0;
export const uid = (p = "i") => `${p}_${Date.now().toString(36)}_${(seq++).toString(36)}`;
