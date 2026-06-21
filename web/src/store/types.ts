import type { Asset, Task, ToolCardData, ModelEntry, AdaptPipelineItem, VariantsGroupItem } from "@/lib/types";

// WaitLevel tiers the post-send loading state: "p1" is the lightweight default
// micro-hint ("正在启动深度思考…"), "p2" is the heavier static fallback shown
// only when the turn is known non-streaming (backend signal) or the first model
// increment has not arrived within the P1 timeout.
export type WaitLevel = "p1" | "p2";

// Chat log is an ordered list of items: user/assistant bubbles, reasoning
// blocks, and tool cards. Each carries a stable id for keyed rendering.
export type ChatItem =
  | { kind: "user"; id: string; text: string }
  | { kind: "assistant"; id: string; text: string; streaming: boolean }
  | { kind: "reasoning"; id: string; text: string; collapsed: boolean; done: boolean }
  | {
      kind: "analysis";
      id: string;
      text: string;
      collapsed: boolean;
      done: boolean;
      // Confirmation window (only set on a fresh, non-cached live analysis). The
      // backend emits a "summary_confirm" signal carrying cacheKey after the report
      // streams done; the panel then runs a 3s countdown the user can edit during.
      cacheKey?: string;
      confirming?: boolean; // in the editable confirmation window
      confirmed?: boolean; // user submitted / countdown expired — panel disabled
      editing?: boolean; // user opened the inline editor (countdown paused)
      secondsLeft?: number; // countdown remaining; undefined once paused (editing)
      reanalyzing?: boolean; // fresh grok analysis in progress (disables submit)
    }
  | { kind: "tool"; id: string; tool: ToolCardData }
  | {
      // Structured marketing copy produced by generate_copy, rendered as a
      // grouped card (title / slogans / selling points / platform copy) the user
      // can read and copy. Carried in the tool_result event's data fields.
      kind: "copy";
      id: string;
      title?: string;
      slogans?: string[];
      sellingPoints?: string[];
      platformCopy?: string;
    }
  | AdaptPipelineItem
  | VariantsGroupItem
  | { kind: "capsule"; id: string; question: string; options: CapsuleOption[]; answered: boolean }
  | { kind: "follow_up"; id: string; message: string; options: CapsuleOption[]; dismissed: boolean }
  | { kind: "loading"; id: string; level: WaitLevel };

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
  sizeIds?: string[];
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
