// Package agent implements the conversation orchestration layer: an Eino
// ReAct agent that recognizes a whitelist of intents and dispatches them to
// image-generation, cropping and download tools. It also owns the per-session
// sliding context window that keeps the LLM prompt within a token budget.
package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"
)

// approxTokens gives a cheap, deterministic token estimate. We do not need
// exact tokenization for windowing decisions, only a stable monotonic proxy.
//
// A single ~4-chars-per-token rule badly underestimates CJK text: a Chinese
// character is typically ≈1 token on its own (sometimes more), whereas Latin
// text runs closer to ~4 chars/token. We therefore split the estimate: CJK
// runes count as ~1 token each, and the remaining (ASCII/Latin/whitespace)
// runes are bundled at ~4 per token. This keeps the budget honest for the
// Chinese-heavy traffic this app actually sees.
func approxTokens(s string) int {
	if s == "" {
		return 0
	}
	cjk, other := 0, 0
	for _, r := range s {
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

// isCJK reports whether r is a CJK ideograph or common CJK symbol/kana that a
// tokenizer would charge at roughly one token apiece.
func isCJK(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Extension A
		return true
	case r >= 0x3000 && r <= 0x303F: // CJK symbols & punctuation
		return true
	case r >= 0x3040 && r <= 0x30FF: // Hiragana + Katakana
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // Fullwidth forms
		return true
	default:
		return false
	}
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
		content = truncateSemantic(content, 200)
		if content == "" {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", role, content)
	}
	return strings.TrimRight(b.String(), "\n")
}

// truncateSemantic shortens s to at most maxRunes runes WITHOUT splitting a
// multi-byte UTF-8 sequence (byte-slicing CJK text corrupts the trailing rune).
// When truncation is needed it prefers to cut at the last sentence/clause
// boundary (。！？.!? or whitespace) within the limit so the summary stays
// readable rather than chopping mid-word.
func truncateSemantic(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	cut := r[:maxRunes]
	// Walk back to the last natural boundary within the kept slice.
	for i := len(cut) - 1; i > maxRunes/2; i-- {
		switch cut[i] {
		case '。', '！', '？', '.', '!', '?', '\n', ' ', '，', ',', '；', ';':
			return strings.TrimRight(string(cut[:i+1]), " \n") + "…"
		}
	}
	return string(cut) + "…"
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
	// lastAssetOp records the most recent "edit lineage" (source asset → output
	// asset) seen among compressed turns, so summarization doesn't erase which
	// image the conversation has been iterating on. Updated as turns are folded;
	// rendered as a structured "[最近编辑: source=… → output=…]" anchor in the
	// summary (summary-asset-anchor). Either side may be empty when only one is
	// recoverable from the window.
	lastAssetOp assetOp
	// hasEverCalledTool is true once any assistant message with tool_calls is
	// appended. compressLocked uses it to enforce the tool-primed invariant:
	// keep ≥1 complete assistant{tool_calls}→role:tool exchange in recent so
	// the model's few-shot signal for tool use is never erased by compression.
	// Reset naturally when a new Window is created (e.g. after ResetContext).
	hasEverCalledTool bool
	// pendingCompressions buffers a snapshot of each compression cycle performed
	// during the most recent Append, so the caller (which holds the turn's trace
	// context) can drain and log them. compressLocked has no ctx of its own.
	pendingCompressions []CompressionEvent
}

// CompressionEvent is a before/after snapshot of one compression cycle, surfaced
// for diagnostic logging (chasing hallucinations caused by a truncated context).
type CompressionEvent struct {
	// BeforeMsgs / AfterMsgs are the recent-message counts around the fold.
	BeforeMsgs int
	AfterMsgs  int
	// Folded is how many messages were summarized away this cycle.
	Folded int
	// SummaryLen is the byte length of the resulting summary message.
	SummaryLen int
	// ToolExchangeKept reports whether the post-fold window still contains a
	// complete assistant{tool_calls}→role:tool anchor (the few-shot invariant).
	ToolExchangeKept bool
}

// DrainCompressions returns and clears the compression snapshots buffered since
// the last call. The caller logs them with the turn's trace context.
func (w *Window) DrainCompressions() []CompressionEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.pendingCompressions
	w.pendingCompressions = nil
	return out
}

// assetOp is the source→output lineage of one edit operation. SourceID comes
// from an edit tool-call's source_asset_id argument; OutputID is resolved from a
// "[上次产物: 图N]" annotation against the workspace numbering map in the same
// folded slice.
type assetOp struct {
	SourceID string
	OutputID string
}

func (op assetOp) zero() bool { return op.SourceID == "" && op.OutputID == "" }

// parseWorkspaceLabels parses a "[工作区: 图1=abc(生成), 视频1=def(视频)]" prefix into
// a label→id map ("图1"→"abc"). Returns nil when no workspace prefix is present.
func parseWorkspaceLabels(content string) map[string]string {
	start := strings.Index(content, "[工作区: ")
	if start < 0 {
		return nil
	}
	rest := content[start+len("[工作区: "):]
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return nil
	}
	body := rest[:end]
	out := map[string]string{}
	for _, entry := range strings.Split(body, ",") {
		entry = strings.TrimSpace(entry)
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 {
			continue
		}
		lbl := entry[:eq]
		idAndKind := entry[eq+1:]
		// Strip the trailing "(类型)" annotation.
		if p := strings.IndexByte(idAndKind, '('); p >= 0 {
			idAndKind = idAndKind[:p]
		}
		id := strings.TrimSpace(idAndKind)
		if lbl != "" && id != "" {
			out[lbl] = id
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseLastProducedLabel extracts the "图N"/"视频N" label from a
// "[上次产物: 图N]" annotation, or "" when absent.
func parseLastProducedLabel(content string) string {
	start := strings.Index(content, "[上次产物: ")
	if start < 0 {
		return ""
	}
	rest := content[start+len("[上次产物: "):]
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// parseSourceAssetID pulls source_asset_id out of an edit tool-call's JSON
// arguments, or "" when the field is absent/unparseable.
func parseSourceAssetID(argsJSON string) string {
	if argsJSON == "" {
		return ""
	}
	var probe struct {
		SourceAssetID string `json:"source_asset_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &probe); err != nil {
		return ""
	}
	return probe.SourceAssetID
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
	if len(m.ToolCalls) > 0 {
		w.hasEverCalledTool = true
	}
	w.recent = append(w.recent, m)
	w.compressLocked()
}

// recentHasToolExchange reports whether msgs contains at least one complete
// assistant{tool_calls}→role:tool exchange — a structural few-shot anchor
// that keeps the model calling tools rather than drifting to prose replies.
func recentHasToolExchange(msgs []*schema.Message) bool {
	for i, m := range msgs {
		if m.Role == schema.Assistant && len(m.ToolCalls) > 0 {
			if i+1 < len(msgs) && msgs[i+1].Role == schema.Tool {
				return true
			}
		}
	}
	return false
}

// HasToolExchange reports whether the current recent window contains at least
// one complete assistant{tool_calls}→role:tool exchange. Used in diagnostic
// logging to verify the tool-primed invariant holds after each compression.
func (w *Window) HasToolExchange() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return recentHasToolExchange(w.recent)
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
		// Never leave recent starting with an orphaned tool-result message: a
		// role:"tool" with no preceding assistant{tool_calls} is an invalid
		// sequence that Anthropic/OpenAI reject, causing the provider to fail or
		// silently drop messages — which reverse-trains the model to stop using
		// tools. Advance foldCount until recent[0] is not a tool message.
		for foldCount < len(w.recent) && w.recent[foldCount].Role == schema.Tool {
			foldCount++
		}
		if foldCount >= len(w.recent) {
			break // cannot split cleanly; skip this compression cycle
		}
		// Tool-primed back-off: when the session has ever called a tool, prefer
		// a foldCount that leaves recent with at least one complete tool exchange
		// (the model's few-shot anchor). Walk foldCount backward; if no such
		// position exists (foldCount reaches 0) restore the original value and
		// compress normally — compressing is always better than blocking forever.
		if w.hasEverCalledTool && !recentHasToolExchange(w.recent[foldCount:]) {
			orig := foldCount
			for foldCount > 0 && !recentHasToolExchange(w.recent[foldCount:]) {
				foldCount--
			}
			if foldCount == 0 {
				foldCount = orig // best-effort: no valid position, compress anyway
			}
		}
		older := w.recent[:foldCount]

		// Preserve the most recent edit lineage (source→output) among the folded
		// turns before they are summarized away (summary-asset-anchor). Merge
		// per-field rather than overwrite: source and output often arrive in
		// separate compression batches (the edit tool-call vs the next turn's
		// [上次产物] annotation), so a whole-struct overwrite would drop one side.
		if op := extractAssetOp(older); !op.zero() {
			if op.SourceID != "" {
				w.lastAssetOp.SourceID = op.SourceID
			}
			if op.OutputID != "" {
				w.lastAssetOp.OutputID = op.OutputID
			}
		}

		merged := older
		if w.summary != nil {
			merged = append([]*schema.Message{w.summary}, older...)
		}
		body := w.summarize(merged)
		// Strip any prior anchor line so re-summarizing the old summary doesn't
		// duplicate it, then append the freshest anchor once.
		body = stripAssetAnchor(body)
		if !w.lastAssetOp.zero() {
			body += "\n" + renderAssetAnchor(w.lastAssetOp)
		}
		w.summary = schema.SystemMessage(body)
		beforeMsgs := len(w.recent)
		w.recent = append([]*schema.Message{}, w.recent[foldCount:]...)
		w.pendingCompressions = append(w.pendingCompressions, CompressionEvent{
			BeforeMsgs:       beforeMsgs,
			AfterMsgs:        len(w.recent),
			Folded:           foldCount,
			SummaryLen:       len(body),
			ToolExchangeKept: recentHasToolExchange(w.recent),
		})
	}
}

// assetAnchorPrefix marks the structured edit-lineage line appended to a summary.
const assetAnchorPrefix = "[最近编辑:"

// renderAssetAnchor formats an edit lineage as a single structured line. Either
// side may be empty (only the present side is rendered).
func renderAssetAnchor(op assetOp) string {
	switch {
	case op.SourceID != "" && op.OutputID != "":
		return assetAnchorPrefix + " source=" + op.SourceID + " → output=" + op.OutputID + "]"
	case op.OutputID != "":
		return assetAnchorPrefix + " output=" + op.OutputID + "]"
	default:
		return assetAnchorPrefix + " source=" + op.SourceID + "]"
	}
}

// stripAssetAnchor removes a trailing "[最近编辑: …]" line from a summary body so
// re-compression does not accumulate stale anchors.
func stripAssetAnchor(body string) string {
	lines := strings.Split(body, "\n")
	kept := lines[:0]
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), assetAnchorPrefix) {
			continue
		}
		kept = append(kept, ln)
	}
	return strings.TrimRight(strings.Join(kept, "\n"), "\n")
}

// extractAssetOp scans a folded message slice for the most recent edit lineage:
// the source_asset_id carried in an edit tool-call's arguments, paired with the
// output asset resolved from a "[上次产物: 图N]" annotation against the workspace
// numbering map present in the same slice. Returns a zero assetOp when neither is
// recoverable (e.g. a pure-text conversation with no edits).
func extractAssetOp(msgs []*schema.Message) assetOp {
	var op assetOp
	label := map[string]string{} // "图N"/"视频N" -> asset id, latest map wins
	for _, m := range msgs {
		if m == nil {
			continue
		}
		// Refresh the numbering map from any "[工作区: …]" prefix on this message.
		if lm := parseWorkspaceLabels(m.Content); len(lm) > 0 {
			label = lm
		}
		// Output: resolve "[上次产物: 图N]" against the current map.
		if lbl := parseLastProducedLabel(m.Content); lbl != "" {
			if id, ok := label[lbl]; ok {
				op.OutputID = id
			}
		}
		// Source: the source_asset_id on an edit tool call.
		for _, tc := range m.ToolCalls {
			if src := parseSourceAssetID(tc.Function.Arguments); src != "" {
				op.SourceID = src
			}
		}
	}
	return op
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

// SystemTokens returns the token estimate for the system prompt alone. Used by
// the frontend to compute the net conversation usage (total minus base cost),
// so clearing context shows 0% rather than ~19%.
func (w *Window) SystemTokens() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return messageTokens(w.system)
}
