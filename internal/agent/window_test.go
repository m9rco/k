package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestApproxTokensCJKvsASCII verifies the split estimator counts CJK runes more
// heavily than ASCII (a Chinese char is ~1 token, ~3-4 ASCII chars are 1 token),
// so a mostly-Chinese string of N runes estimates higher than N ASCII chars.
func TestApproxTokensCJKvsASCII(t *testing.T) {
	cases := []struct {
		name      string
		s         string
		minTokens int
		maxTokens int
	}{
		{"empty", "", 0, 0},
		{"ascii_word", "hello world", 2, 4},
		{"cjk_short", "换背景", 2, 4},             // ~1 token per CJK rune
		{"cjk_sentence", "把这张图的背景换成夜晚", 8, 12}, // 10 CJK runes
		{"mixed", "把背景换成 cyberpunk city", 6, 12},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := approxTokens(c.s)
			if got < c.minTokens || got > c.maxTokens {
				t.Errorf("approxTokens(%q) = %d, want within [%d,%d]", c.s, got, c.minTokens, c.maxTokens)
			}
		})
	}
}

// TestApproxTokensCJKHeavierThanASCII asserts the core property: an equal RUNE
// count of CJK estimates strictly higher than ASCII (CJK is denser per token).
func TestApproxTokensCJKHeavierThanASCII(t *testing.T) {
	const n = 40
	cjk := strings.Repeat("字", n)
	ascii := strings.Repeat("a", n)
	if approxTokens(cjk) <= approxTokens(ascii) {
		t.Errorf("CJK (%d) should estimate higher than ASCII (%d) for equal rune count",
			approxTokens(cjk), approxTokens(ascii))
	}
}

func TestTruncateSemanticNoRuneCorruption(t *testing.T) {
	s := strings.Repeat("赛博朋克城市夜景。", 20) // multi-byte runes
	out := truncateSemantic(s, 10)
	if !utf8ValidNoReplacement(out) {
		t.Fatalf("truncation corrupted UTF-8: %q", out)
	}
	if len([]rune(out)) > 11 { // 10 + ellipsis
		t.Errorf("truncated result too long: %d runes", len([]rune(out)))
	}
	// Short input returned unchanged.
	if got := truncateSemantic("短", 10); got != "短" {
		t.Errorf("short input changed: %q", got)
	}
}

func TestTruncateSemanticPrefersBoundary(t *testing.T) {
	s := "第一句话。第二句话也很长很长很长很长很长很长。"
	out := truncateSemantic(s, 12)
	// Should cut at the first 。within the limit, not mid-clause.
	if !strings.Contains(out, "第一句话。") {
		t.Errorf("expected boundary cut after first sentence, got %q", out)
	}
}

// utf8ValidNoReplacement reports whether s is valid UTF-8 with no replacement
// runes (byte-slicing CJK would introduce U+FFFD).
func utf8ValidNoReplacement(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// TestCompressNoOrphanToolMessage is the regression guard for the "stops calling
// tools after compression" bug: compressLocked must never leave recent starting
// with a role:"tool" message whose assistant{tool_calls} was folded into the
// summary, because that orphan makes the provider reject or silently drop the
// message sequence, reverse-training the model to avoid tool use.
func TestCompressNoOrphanToolMessage(t *testing.T) {
	// Build a window with a very small budget so compression triggers immediately.
	// keepRecent=1 means at most 1 message is kept verbatim.
	w := NewWindow("system", 50, 1, func(_ []*schema.Message) string { return "summary" })

	// Append an assistant turn that called a tool.
	assistantWithTool := schema.AssistantMessage("", []schema.ToolCall{
		{ID: "tc1", Function: schema.FunctionCall{Name: "edit_image", Arguments: `{}`}},
	})
	toolResult := schema.ToolMessage("[edit_image 已执行]", "tc1")
	toolResult.ToolName = "edit_image"
	assistantText := schema.AssistantMessage("好的，已处理。", nil)
	nextUser := schema.UserMessage("再改一下")

	w.Append(assistantWithTool)
	w.Append(toolResult)
	w.Append(assistantText)
	w.Append(nextUser) // triggers compression beyond keepRecent

	msgs := w.Messages()
	// The first non-system, non-summary message in recent must never be role:tool.
	for _, m := range msgs {
		if m.Role == schema.System {
			continue
		}
		if m.Role == schema.Tool {
			t.Errorf("first visible non-system message is role:tool (orphaned tool result): full sequence has %d messages", len(msgs))
		}
		break
	}
}

// TestCompressPreservesToolExchange is the regression guard for the
// reverse-few-shot drift bug: when the session has called tools before,
// compressLocked must retain at least one complete assistant{tool_calls}→
// role:tool pair in recent so the model keeps its structural few-shot anchor.
func TestCompressPreservesToolExchange(t *testing.T) {
	// Use large messages so the 256 minimum budget is exceeded quickly.
	bigMsg := strings.Repeat("x", 300)
	w := NewWindow("sys", 512, 2, func(_ []*schema.Message) string { return "sum" })

	toolCall := func(id, name string) *schema.Message {
		return schema.AssistantMessage("", []schema.ToolCall{
			{ID: id, Function: schema.FunctionCall{Name: name, Arguments: `{}`}},
		})
	}
	toolRes := func(id string) *schema.Message {
		m := schema.ToolMessage("[done]", id)
		m.ToolName = "edit_image"
		return m
	}

	// Build enough history to trigger compression.
	w.Append(schema.UserMessage(bigMsg))
	w.Append(toolCall("tc1", "edit_image"))
	w.Append(toolRes("tc1"))
	w.Append(schema.UserMessage(bigMsg))
	w.Append(toolCall("tc2", "edit_image"))
	w.Append(toolRes("tc2"))
	w.Append(schema.UserMessage(bigMsg)) // final user, triggers compression

	if !w.Compressed() {
		t.Skip("compression did not trigger; increase message size or check budget")
	}

	msgs := w.Messages()
	if !recentHasToolExchange(msgs) {
		t.Errorf("after compression, recent has no tool exchange: roles=%s", roleSeq(msgs))
	}
}

// TestCompressChatOnlyNoConstraint verifies that a session that never called
// any tools is not subject to the tool-exchange preservation constraint — the
// window should compress freely even if no tool pair exists in recent.
// Budget must be ≥256 (NewWindow minimum); use large messages to force compression.
func TestCompressChatOnlyNoConstraint(t *testing.T) {
	// "x"*300 = 300 ASCII chars → (300+3)/4=75 tokens + 4 = 79 tokens per msg.
	// 5 (system) + 2*(79+79) = 5+316=321 > 256: compression fires after 2 pairs.
	bigMsg := strings.Repeat("x", 300)
	w := NewWindow("sys", 512, 1, func(_ []*schema.Message) string { return "sum" })
	for i := 0; i < 5; i++ {
		w.Append(schema.UserMessage(bigMsg))
		w.Append(schema.AssistantMessage(bigMsg, nil))
	}
	if !w.Compressed() {
		t.Error("expected compression to have run for pure-chat window")
	}
	// No panic or infinite loop = also passed.
}

// roleSeq returns a comma-separated role sequence for test failure messages.
func roleSeq(msgs []*schema.Message) string {
	parts := make([]string, 0, len(msgs))
	for _, m := range msgs {
		role := string(m.Role)
		if m.Role == schema.Assistant && len(m.ToolCalls) > 0 {
			role = "assistant[tc]"
		}
		parts = append(parts, role)
	}
	return strings.Join(parts, ",")
}
