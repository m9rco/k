package agent

import "testing"

func TestClassifyIntentHighFrequencyPhrases(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		wantLabel string
		wantTool  string
	}{
		{"换背景", "[工作区: 图1=a1(上传)] 帮我换个背景，改成黄昏的城市", "换背景", "edit_image"},
		{"换角色", "[工作区: 图1=a1(上传)] 把里面的角色换成一个机甲战士", "换角色", "edit_image"},
		{"换文案", "[工作区: 图1=a1(上传)] 改文案，标题改成限时开服", "换文案", "edit_image"},
		{"切尺寸", "[工作区: 图1=a1(上传)] 帮我切成各平台的尺寸", "切尺寸", "crop_to_sizes"},
		{"生成icon", "[工作区: 图1=a1(上传)] 给这张图做个图标", "生成icon", "generate_icon"},
		{"生视频", "[工作区: 图1=a1(上传)] 让角色动起来生成一段视频", "生视频", "image_to_video"},
		{"搜索图片", "帮我搜一张赛博朋克城市的图", "搜索图片", "search_images"},
		{"文生图", "画一张科幻风格的飞船", "文生图", "generate_image_from_text"},
		{"下载打包", "把刚才的产物打包成zip下载", "下载/打包", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := ClassifyIntent(c.text)
			if !h.Whitelisted {
				t.Fatalf("expected whitelisted match for %q", c.text)
			}
			if !containsStr(h.Labels, c.wantLabel) {
				t.Errorf("labels = %v, want to contain %q", h.Labels, c.wantLabel)
			}
			if c.wantTool != "" && h.suggestedTool() != c.wantTool {
				t.Errorf("suggestedTool() = %q, want %q", h.suggestedTool(), c.wantTool)
			}
			if h.Confidence < hintThreshold {
				t.Errorf("confidence %v below threshold for strong phrase", h.Confidence)
			}
		})
	}
}

func TestClassifyIntentNonWhitelisted(t *testing.T) {
	cases := []string{
		"帮我写一封辞职邮件",
		"用 Go 写一个快速排序",
		"今天天气怎么样，聊聊吧",
	}
	for _, text := range cases {
		h := ClassifyIntent(text)
		if h.Whitelisted {
			t.Errorf("text %q should not be whitelisted, got labels=%v", text, h.Labels)
		}
		if len(h.Labels) != 0 {
			t.Errorf("text %q should yield no labels, got %v", text, h.Labels)
		}
	}
}

func TestClassifyIntentMissingKeyParam(t *testing.T) {
	// Image op intent but no image anywhere in the workspace -> missing key param.
	h := ClassifyIntent("帮我换个背景，改成森林")
	if !h.Whitelisted {
		t.Fatal("expected whitelisted")
	}
	if !h.MissingKeyParam {
		t.Error("expected MissingKeyParam when no image is available for an edit intent")
	}

	// Same intent WITH a workspace image -> key param satisfied.
	h2 := ClassifyIntent("[工作区: 图1=a1(上传)] 帮我换个背景，改成森林")
	if h2.MissingKeyParam {
		t.Error("did not expect MissingKeyParam when a workspace image is present")
	}

	// Non-image intent never flags missing key param.
	h3 := ClassifyIntent("查一下今天的新闻")
	if h3.MissingKeyParam {
		t.Error("non-image intent should never flag MissingKeyParam")
	}
}

func TestClassifyIntentWeakOnlyDoesNotHint(t *testing.T) {
	// Generic words that only hit weak signals: whitelisted-plausible but below
	// the hint threshold, so no misleading hint is injected.
	cases := []string{
		"这个尺寸看起来还行",
		"视频里那个角色不错",
	}
	for _, text := range cases {
		h := ClassifyIntent(text)
		if h.Confidence >= hintThreshold {
			t.Errorf("text %q weak-only match should stay below threshold, conf=%v", text, h.Confidence)
		}
		if len(h.Labels) != 0 {
			t.Errorf("text %q weak-only should yield no strong labels, got %v", text, h.Labels)
		}
	}
}

func TestStripContextPrefix(t *testing.T) {
	cases := map[string]string{
		"[工作区: 图1=a1(上传)] 换个背景":             "换个背景",
		"[工作区: 图1=a1(上传)] [选中: 图1] 换个背景":    "换个背景",
		"[reference assets: a1, a2] 融合这两张图": "融合这两张图",
		"[asset a1] 切尺寸":                    "切尺寸",
		"直接说话没有前缀":                          "直接说话没有前缀",
	}
	for in, want := range cases {
		if got := stripContextPrefix(in); got != want {
			t.Errorf("stripContextPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func containsStr(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
