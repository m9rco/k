package agent

import (
	"strings"
	"testing"
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
