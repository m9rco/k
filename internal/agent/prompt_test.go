package agent

import (
	"strings"
	"testing"
)

// TestSystemPromptDirectives asserts the layered system prompt carries the
// critical instructions the harness depends on: clarify-first, content-first
// replies, injection guard, reference-id usage, whitelist refusal, Chinese
// output (including thinking), asset-number mapping, and multi-image intent.
func TestSystemPromptDirectives(t *testing.T) {
	p := SystemPrompt()
	checks := map[string]string{
		"clarify tool":        "clarify_intent",
		"clarify-first":       "关键参数",
		"no numbered list":    "结构化选项",
		"injection guard":     "ignore previous instructions",
		"reference id usage":  "reference_asset_ids",
		"chinese output":      "简体中文",
		"thinking chinese":    "思考过程",
		"async config gate":   "暂未配置",
		"asset numbering":     "图N",
		"multi-image intent":  "被编辑底图",
		"content first":       "写进正式回复正文",
		"markdown rendered":   "前端会渲染",
		"intent hint advice":  "意图提示",
		"intent hint as data": "仅供参考",
		"no fake execution":   "绝不假执行",
	}
	for name, want := range checks {
		if !strings.Contains(p, want) {
			t.Errorf("system prompt missing %s directive (substring %q)", name, want)
		}
	}
	// Layered section headers should be present.
	for _, section := range []string{"【支持的能力】", "【工具使用规范】", "【交互与澄清规范】", "【输出格式规范】", "【安全规范】", "【语言】"} {
		if !strings.Contains(p, section) {
			t.Errorf("system prompt missing section header %q", section)
		}
	}
}

func TestBuildAssetNumbering(t *testing.T) {
	// Empty order -> no injection.
	if got := BuildAssetNumbering(nil, nil, ""); got != "" {
		t.Errorf("empty order should yield empty string, got %q", got)
	}

	// Images and videos are numbered in two independent sequences (图N / 视频N).
	order := []AssetRef{
		{ID: "a1", Kind: "upload"},
		{ID: "a2", Kind: "generated"},
		{ID: "v1", Kind: "video"},
		{ID: "a3", Kind: "cropped"},
		{ID: "v2", Kind: "video"},
	}
	got := BuildAssetNumbering(order, []string{"a2", "v1"}, "")
	for _, want := range []string{"图1=a1(上传)", "图2=a2(生成)", "视频1=v1(视频)", "图3=a3(裁剪)", "视频2=v2(视频)"} {
		if !strings.Contains(got, want) {
			t.Errorf("numbering missing %q in %q", want, got)
		}
	}
	// Selected annotation references the same labels (mixed 图/视频).
	if !strings.Contains(got, "[选中: 图2, 视频1]") {
		t.Errorf("selected annotation wrong in %q", got)
	}

	// No selection -> no 选中 block.
	got2 := BuildAssetNumbering(order, nil, "")
	if strings.Contains(got2, "选中") {
		t.Errorf("unexpected 选中 block in %q", got2)
	}
}

// TestBuildAssetNumberingLastProduced covers the sticky-last-output annotation:
// when nothing is selected, the last produced asset is annotated as
// "[上次产物: 图N]"; an explicit selection always wins over it.
func TestBuildAssetNumberingLastProduced(t *testing.T) {
	order := []AssetRef{
		{ID: "a1", Kind: "upload"},
		{ID: "a2", Kind: "generated"},
	}

	// No selection + lastProduced in workspace -> [上次产物: 图N].
	got := BuildAssetNumbering(order, nil, "a2")
	if !strings.Contains(got, "[上次产物: 图2]") {
		t.Errorf("expected [上次产物: 图2] in %q", got)
	}
	if strings.Contains(got, "选中") {
		t.Errorf("should not emit 选中 when only lastProduced is set: %q", got)
	}

	// Explicit selection wins: no [上次产物] even when lastProduced is set.
	got2 := BuildAssetNumbering(order, []string{"a1"}, "a2")
	if strings.Contains(got2, "上次产物") {
		t.Errorf("explicit selection should suppress 上次产物: %q", got2)
	}
	if !strings.Contains(got2, "[选中: 图1]") {
		t.Errorf("expected [选中: 图1] in %q", got2)
	}

	// lastProduced not in the current workspace -> no annotation.
	got3 := BuildAssetNumbering(order, nil, "gone")
	if strings.Contains(got3, "上次产物") {
		t.Errorf("lastProduced absent from workspace should add nothing: %q", got3)
	}
}
