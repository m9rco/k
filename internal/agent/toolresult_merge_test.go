package agent

import (
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

// TestMergeToolResultFields_CopyJSON verifies a synchronous tool's structured
// JSON (generate_copy) is flattened into the tool_result event data so the
// frontend can render the copy card. This is the fix for "tool spins to done but
// no copy card appears".
func TestMergeToolResultFields_CopyJSON(t *testing.T) {
	dst := map[string]any{"name": "generate_copy", "status": "done", "summary": "{...}"}
	out := &tool.CallbackOutput{Response: `{"status":"done","title":"开服盛典","slogans":["来玩","上线"],"selling_points":["多人","福利"],"platform_copy":"立即体验"}`}
	mergeToolResultFields(dst, out)

	if dst["title"] != "开服盛典" {
		t.Errorf("title not flattened: %v", dst["title"])
	}
	sl, ok := dst["slogans"].([]any)
	if !ok || len(sl) != 2 {
		t.Errorf("slogans not flattened as array: %v", dst["slogans"])
	}
	if dst["platform_copy"] != "立即体验" {
		t.Errorf("platform_copy not flattened: %v", dst["platform_copy"])
	}
	if dst["name"] != "generate_copy" {
		t.Errorf("name must stay authoritative, got %v", dst["name"])
	}
}

// TestMergeToolResultFields_NonJSONIgnored verifies a friendly ack string (an
// async ReturnDirectly tool's standalone reply) is left untouched — Unmarshal
// fails and the human-friendly summary is preserved.
func TestMergeToolResultFields_NonJSONIgnored(t *testing.T) {
	dst := map[string]any{"name": "generate_variants", "status": "done", "summary": "好的，正在生成变体"}
	out := &tool.CallbackOutput{Response: "好的，正在为这张图生成 4 个不同风格的变体，产物会陆续出现在左侧工作区。"}
	mergeToolResultFields(dst, out)

	if len(dst) != 3 {
		t.Errorf("non-JSON response must not add keys, got %d keys: %v", len(dst), dst)
	}
	if dst["summary"] != "好的，正在生成变体" {
		t.Errorf("summary clobbered by non-JSON merge: %v", dst["summary"])
	}
}

// TestMergeToolResultFields_NameNotOverridden guards that a result carrying a
// "name" field cannot rename the tool (name comes from the trusted RunInfo).
func TestMergeToolResultFields_NameNotOverridden(t *testing.T) {
	dst := map[string]any{"name": "overlay_text", "status": "done"}
	out := &tool.CallbackOutput{Response: `{"name":"evil","status":"done","asset_id":"a9"}`}
	mergeToolResultFields(dst, out)

	if dst["name"] != "overlay_text" {
		t.Errorf("name must not be overridden by result, got %v", dst["name"])
	}
	if dst["asset_id"] != "a9" {
		t.Errorf("asset_id not flattened: %v", dst["asset_id"])
	}
}

// TestMergeToolResultFields_NilSafe verifies nil output/dst are no-ops.
func TestMergeToolResultFields_NilSafe(t *testing.T) {
	mergeToolResultFields(nil, &tool.CallbackOutput{Response: "{}"}) // must not panic
	dst := map[string]any{"name": "x"}
	mergeToolResultFields(dst, nil)
	if len(dst) != 1 {
		t.Errorf("nil output must be a no-op, got %v", dst)
	}
}
