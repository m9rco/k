// Package agent implements the conversation orchestration layer: an Eino
// ReAct agent that recognizes a whitelist of intents and dispatches them to
// image-generation, cropping and download tools. It also owns the per-session
// sliding context window that keeps the LLM prompt within a token budget.
package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"
)

// approxTokens gives a cheap, deterministic token estimate. We do not need
// exact tokenization for windowing decisions, only a stable monotonic proxy.
// The heuristic ~4 chars per token is the common rule of thumb for English/CJK
// mixed text and is good enough to bound the window.
func approxTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len([]rune(s)) + 3) / 4
}

func messageTokens(m *schema.Message) int {
	n := approxTokens(m.Content)
	for _, tc := range m.ToolCalls {
		n += approxTokens(tc.Function.Name) + approxTokens(tc.Function.Arguments)
	}
	// Role and structural overhead.
	return n + 4
}

// Summarizer compresses a slice of older messages into a single summary string.
// It is injected so the window can be unit-tested without a live model.
type Summarizer func(older []*schema.Message) string

// defaultSummarizer produces a deterministic, extractive summary. It is used as
// a fallback and in tests; a model-backed summarizer can be supplied instead.
func defaultSummarizer(older []*schema.Message) string {
	var b strings.Builder
	b.WriteString("Earlier conversation summary:\n")
	for _, m := range older {
		role := string(m.Role)
		content := strings.TrimSpace(m.Content)
		if content == "" && len(m.ToolCalls) > 0 {
			names := make([]string, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				names = append(names, tc.Function.Name)
			}
			content = "(called tools: " + strings.Join(names, ", ") + ")"
		}
		if len(content) > 200 {
			content = content[:200] + "…"
		}
		if content == "" {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", role, content)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Window is a per-session sliding context window. It retains the system prompt
// plus the most recent turns verbatim, and compresses older turns into a single
// summary message once the running token estimate exceeds the budget.
//
// Large tool results (e.g. image bytes) must never be stored here; callers pass
// a reference id via AppendToolRef instead so the raw payload stays out of the
// LLM context.
type Window struct {
	mu         sync.Mutex
	system     *schema.Message
	summary    *schema.Message // nil until first compression
	recent     []*schema.Message
	budget     int
	keepRecent int // minimum recent messages to retain verbatim
	summarize  Summarizer
}

// NewWindow creates a window bounded by tokenBudget. keepRecent is the minimum
// number of trailing messages kept verbatim even under pressure.
func NewWindow(system string, tokenBudget, keepRecent int, summarize Summarizer) *Window {
	if keepRecent < 1 {
		keepRecent = 1
	}
	if tokenBudget < 256 {
		tokenBudget = 256
	}
	if summarize == nil {
		summarize = defaultSummarizer
	}
	return &Window{
		system:     schema.SystemMessage(system),
		budget:     tokenBudget,
		keepRecent: keepRecent,
		summarize:  summarize,
	}
}

// Append adds a normal conversation message and compresses if over budget.
func (w *Window) Append(m *schema.Message) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.recent = append(w.recent, m)
	w.compressLocked()
}

// AppendToolRef appends a tool result as a compact reference instead of its raw
// payload, keeping large binary/base64 data out of the LLM context (D3).
func (w *Window) AppendToolRef(toolCallID, toolName, refID, shortDesc string) {
	content := fmt.Sprintf("[%s result ref=%s] %s", toolName, refID, shortDesc)
	m := schema.ToolMessage(content, toolCallID)
	m.ToolName = toolName
	w.Append(m)
}

// compressLocked summarizes the oldest messages beyond keepRecent when the
// total estimate exceeds the budget. Must hold w.mu.
func (w *Window) compressLocked() {
	for w.totalTokensLocked() > w.budget && len(w.recent) > w.keepRecent {
		// Take the oldest non-retained messages and fold them into the summary.
		foldCount := len(w.recent) - w.keepRecent
		older := w.recent[:foldCount]

		merged := older
		if w.summary != nil {
			merged = append([]*schema.Message{w.summary}, older...)
		}
		w.summary = schema.SystemMessage(w.summarize(merged))
		w.recent = append([]*schema.Message{}, w.recent[foldCount:]...)
	}
}

func (w *Window) totalTokensLocked() int {
	n := messageTokens(w.system)
	if w.summary != nil {
		n += messageTokens(w.summary)
	}
	for _, m := range w.recent {
		n += messageTokens(m)
	}
	return n
}

// Messages returns the current windowed messages ready for a model call:
// system, optional summary, then recent verbatim turns.
func (w *Window) Messages() []*schema.Message {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*schema.Message, 0, len(w.recent)+2)
	out = append(out, w.system)
	if w.summary != nil {
		out = append(out, w.summary)
	}
	out = append(out, w.recent...)
	return out
}

// EstimateTokens reports the current windowed token estimate.
func (w *Window) EstimateTokens() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.totalTokensLocked()
}

// Compressed reports whether any summarization has occurred.
func (w *Window) Compressed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.summary != nil
}
