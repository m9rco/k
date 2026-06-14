import type { Asset, Task, ToolCardData, ModelEntry } from "@/lib/types";

// Chat log is an ordered list of items: user/assistant bubbles, reasoning
// blocks, and tool cards. Each carries a stable id for keyed rendering.
export type ChatItem =
  | { kind: "user"; id: string; text: string }
  | { kind: "assistant"; id: string; text: string; streaming: boolean }
  | { kind: "reasoning"; id: string; text: string; collapsed: boolean; done: boolean }
  | { kind: "tool"; id: string; tool: ToolCardData }
  | { kind: "capsule"; id: string; question: string; options: CapsuleOption[]; answered: boolean }
  | { kind: "follow_up"; id: string; message: string; options: CapsuleOption[]; dismissed: boolean }
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

// QueuedMessage is a user input held while a turn is in flight. It is sent when
// the current turn ends, or promoted/flushed via interrupt by the user.
export interface QueuedMessage {
  id: string;
  text: string;
  ref?: string | string[];
}

export interface AppState {
  sessionId: string;
  connected: boolean;
  chat: ChatItem[];
  assets: Map<string, Asset>;
  tasks: Map<string, Task>;
  selected: Set<string>;
  lossless: boolean;
  // thinking is true between turn_start and turn_end: the agent has acknowledged
  // the message and is working, even before the first model increment arrives.
  thinking: boolean;
  // queue holds messages typed while a turn is in flight; auto-flushed on
  // turn_end, or reordered/interrupt-sent by the user (Cursor-style).
  queue: QueuedMessage[];
  context: { estimatedTokens: number; budget: number; compressed: boolean; systemTokens?: number } | null;
  // models holds the per-scene catalog + current selection + server defaults;
  // loaded lazily when the model picker opens. null until first fetched.
  models: { catalog: Record<string, ModelEntry[]>; selected: Record<string, string>; defaults: Record<string, string> } | null;
}

export const initialState: AppState = {
  sessionId: "",
  connected: false,
  chat: [],
  assets: new Map(),
  tasks: new Map(),
  selected: new Set(),
  lossless: true,
  thinking: false,
  queue: [],
  context: null,
  models: null,
};

let seq = 0;
export const uid = (p = "i") => `${p}_${Date.now().toString(36)}_${(seq++).toString(36)}`;
