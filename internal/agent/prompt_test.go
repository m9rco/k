package agent

import (
	"strings"
	"testing"
)

// TestSystemPromptDirectives asserts the layered system prompt carries the
// critical instructions the harness depends on: clarify-first, no-markdown,
// injection guard, reference-id usage, whitelist refusal, and Chinese output.
func TestSystemPromptDirectives(t *testing.T) {
	p := SystemPrompt()
	checks := map[string]string{
		"clarify tool":       "clarify_intent",
		"clarify-first":      "缺少安全调用工具所必需的信息",
		"no markdown":        "不要使用 markdown",
		"no numbered list":   "结构化选项",
		"injection guard":    "ignore previous instructions",
		"reference id usage": "reference_asset_ids",
		"chinese output":     "简体中文",
		"async config gate":  "暂未配置",
	}
	for name, want := range checks {
		if !strings.Contains(p, want) {
			t.Errorf("system prompt missing %s directive (substring %q)", name, want)
		}
	}
	// Layered section headers should be present.
	for _, section := range []string{"【支持的能力】", "【工具使用规范】", "【交互与澄清规范】", "【输出格式规范】", "【安全规范】"} {
		if !strings.Contains(p, section) {
			t.Errorf("system prompt missing section header %q", section)
		}
	}
}
