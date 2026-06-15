package agent

import "strings"

// IntentHint is the result of the server-side deterministic pre-classification
// that runs BEFORE the chat model is called. It is advisory only: it is used to
// (1) inject a human-readable "意图提示" prefix that nudges a weak model toward
// the right tool, and (2) drive the post-turn remediation loop (clarify vs
// refuse) when the model still makes zero tool calls. The chat model remains the
// final decision-maker and may ignore the hint entirely.
type IntentHint struct {
	// Labels are the confidently-matched whitelist intent labels (strong keyword
	// hits), highest-signal first. Empty when only weak/ambiguous signals matched.
	Labels []string
	// Confidence is the strongest match strength in [0,1]. >= hintThreshold means
	// the hint is worth injecting; weak-only matches stay below it.
	Confidence float64
	// Whitelisted reports whether the text matched ANY whitelist intent at all
	// (even a weak signal). Drives the refuse fallback: a non-whitelisted, empty
	// turn is politely refused; a weakly-matched one is not.
	Whitelisted bool
	// MissingKeyParam is set when a matched intent operates on an image but no
	// image is available in the workspace (no numbering prefix / reference / asset
	// id). Drives the clarify fallback.
	MissingKeyParam bool
}

// intentRule maps one whitelist intent to its keyword signals. strong phrases are
// specific enough to classify on their own (full confidence); weak terms are
// generic (e.g. "尺寸", "视频") and only mark the intent as plausible (half
// confidence) so ambiguous text does not trigger a misleading hint.
type intentRule struct {
	label   string
	tool    string // suggested tool name ("" when no agent tool backs the intent)
	imageOp bool   // requires a source/reference image to act on
	strong  []string
	weak    []string
}

// intentRules is the deterministic classification table. Order is the priority
// used when assembling the (strong) label list.
var intentRules = []intentRule{
	{label: "换背景", tool: "edit_image", imageOp: true,
		strong: []string{"换背景", "改背景", "替换背景", "换个背景", "换一下背景", "背景换", "背景改成", "背景替换", "改成…背景"}},
	{label: "换角色", tool: "edit_image", imageOp: true,
		strong: []string{"换角色", "换人物", "替换角色", "换主角", "换主体", "角色换", "人物换", "换成…角色"}},
	{label: "增加角色", tool: "edit_image", imageOp: true,
		strong: []string{"增加角色", "添加角色", "加个角色", "加一个角色", "多加一个", "增加一位", "增加一个人", "加个人物", "再加一个", "旁边加", "新增角色"}},
	{label: "换文案", tool: "edit_image", imageOp: true,
		strong: []string{"换文案", "改文案", "换文字", "改文字", "替换文案", "文案换", "改字", "换标题", "改标题"}},
	{label: "切尺寸", tool: "crop_to_sizes", imageOp: true,
		strong: []string{"切尺寸", "裁剪", "裁成", "切成", "改尺寸", "适配尺寸", "切图", "裁一下", "各平台尺寸", "广告位"},
		weak:   []string{"尺寸", "各平台", "横版", "竖版"}},
	{label: "生成icon", tool: "generate_icon", imageOp: true,
		strong: []string{"icon", "图标", "做个图标", "生成图标", "提炼图标", "app图标", "游戏图标"}},
	{label: "生视频", tool: "image_to_video", imageOp: true,
		strong: []string{"生视频", "生成视频", "做视频", "动起来", "动效", "做个视频", "让它动", "让角色动", "转成视频", "图生视频"},
		weak:   []string{"视频", "动画"}},
	{label: "搜索图片", tool: "search_images", imageOp: false,
		strong: []string{"搜图", "搜索图片", "找图", "找张图", "找一张", "搜一张", "图片搜索", "搜一张图", "搜张图", "找些图", "找参考图"},
		weak:   []string{"图片", "参考图"}},
	{label: "下载/打包", tool: "", imageOp: false,
		strong: []string{"下载", "打包", "导出", "保存到本地", "打成zip", "打个包"},
		weak:   []string{"zip"}},
	{label: "文生图", tool: "generate_image_from_text", imageOp: false,
		strong: []string{"画一张", "画个", "画张", "生成一张", "来一张", "生成图片", "生成一幅", "做一张图", "生成一张图"},
		weak:   []string{"生成图"}},
}

// hintThreshold is the minimum confidence at which a hint is injected. Strong
// matches score 1.0 (injected); weak-only matches score 0.5 (not injected) so
// ambiguous text yields no misleading hint.
const hintThreshold = 0.6

// ClassifyIntent runs the deterministic pre-classification over a user message.
// userText may carry the leading context prefix(es) ("[工作区: ...]",
// "[reference assets: ...]", "[asset id]") that main.go injects; those are
// stripped before keyword matching but consulted to decide image availability.
//
// It never routes to a tool itself — it only produces advisory signals.
func ClassifyIntent(userText string) IntentHint {
	hasImage := hasWorkspaceImage(userText)
	body := stripContextPrefix(userText)
	lower := strings.ToLower(body)

	var (
		labels      []string
		anyImageOp  bool
		bestConf    float64
		whitelisted bool
	)
	for _, r := range intentRules {
		if matchAny(lower, r.strong) {
			labels = append(labels, r.label)
			whitelisted = true
			if 1.0 > bestConf {
				bestConf = 1.0
			}
			if r.imageOp {
				anyImageOp = true
			}
			continue
		}
		if matchAny(lower, r.weak) {
			whitelisted = true
			if 0.5 > bestConf {
				bestConf = 0.5
			}
		}
	}

	hint := IntentHint{
		Labels:      labels,
		Confidence:  bestConf,
		Whitelisted: whitelisted,
	}
	// Missing key param only matters for confidently-matched image operations:
	// an edit/crop/icon/video intent with no image anywhere to act on.
	if anyImageOp && !hasImage {
		hint.MissingKeyParam = true
	}
	return hint
}

// suggestedTool returns the tool name the matched intents point at, when a single
// confident image/exec tool is implied. Returns "" when there is no match or the
// matches are mixed/unbacked, so the hint can stay generic.
func (h IntentHint) suggestedTool() string {
	tool := ""
	for _, label := range h.Labels {
		for _, r := range intentRules {
			if r.label == label && r.tool != "" {
				if tool == "" {
					tool = r.tool
				} else if tool != r.tool {
					return "" // mixed intents: don't over-specify
				}
			}
		}
	}
	return tool
}

// matchAny reports whether any keyword is a substring of s.
func matchAny(s string, keywords []string) bool {
	for _, k := range keywords {
		if k == "" {
			continue
		}
		if strings.Contains(s, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// hasWorkspaceImage reports whether the message prefix indicates at least one
// image is available to operate on: a workspace numbering entry ("图1="), a
// reference-assets list, a single asset id, or a last-produced annotation
// ("[上次产物: 图N]"). The last-produced case means a recent output exists that
// the model should default to, so MissingKeyParam must NOT fire (sticky-last-
// output: don't ask "which image" when we already know the latest one).
func hasWorkspaceImage(userText string) bool {
	if strings.Contains(userText, "[reference assets:") || strings.Contains(userText, "[asset ") {
		return true
	}
	if strings.Contains(userText, "[上次产物:") {
		return true
	}
	// "[工作区: 图1=..., 视频1=...]" — an image is present iff a 图N entry exists.
	if i := strings.Index(userText, "图"); i >= 0 {
		rest := userText[i+len("图"):]
		if len(rest) > 0 && rest[0] >= '1' && rest[0] <= '9' {
			return true
		}
	}
	return false
}

// stripContextPrefix removes leading "[...]" context groups (工作区 / 选中 /
// reference assets / asset) so keyword matching sees only the user's own words.
func stripContextPrefix(userText string) string {
	s := strings.TrimSpace(userText)
	for strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			break
		}
		s = strings.TrimSpace(s[end+1:])
	}
	return s
}
