package vision

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	applog "gameasset/internal/log"
)

// pointRegionPrompt is the fixed instruction for the CLICK-to-locate path. The
// user clicks one point on the FULL image; the model is told that point's
// normalized coordinate and must (a) identify which object/layer sits under it,
// (b) return that object's tight normalized bounding box, and (c) emit the same
// structured feature block regionPrompt uses, so the result drops straight into
// the edit prompt's region_desc slot. Pure server text — never mixed with user
// input. The %.4f point is interpolated in; the rest is fixed.
const pointRegionPromptTmpl = `你看到的是一整张游戏宣发图。用户在图上点击了一个位置，归一化坐标为 (x=%.4f, y=%.4f)，坐标系以图片左上角为原点 (0,0)、右下角为 (1,1)。

请判断这个点落在画面里的哪个【物体/图层】上（角色、道具、LOGO、文字块、UI 元素、背景元素等），然后：
1. 给出这个物体的紧致包围盒（归一化坐标，box.x/box.y 是左上角，box.w/box.h 是宽高，都在 [0,1]，且尽量贴合该物体的可见轮廓，不要框进无关区域，也不要只框一小角）。
2. 用固定格式描述这个物体的特征。

只输出一个 JSON 对象，不要输出任何解释、前后缀或 Markdown：
{"box":{"x":0.0,"y":0.0,"w":0.0,"h":0.0},"confidence":0,"description":"主体：[类别+一句话外观]\n外观：[材质、颜色、纹理、关键细节，分号分隔]\n文字：[可见文字，没有则写「无」]\n位置：[该物体在整张图中的相对位置]\n必须保留：[改图时绝不可丢失/改变身份的关键视觉特征，分号分隔]"}

confidence 是你对“点中的就是这个物体且包围盒准确”的把握，0-100；点落在空白/背景或无法判断时给低分（<50）。`

// RegionResult is the parsed result of a click-to-locate call: the object's
// normalized bounding box, the structured feature description, and a confidence.
// Box fields ∈ [0,1] with the origin at the image's top-left.
type RegionResult struct {
	Box         Box    `json:"box"`
	Description string `json:"description"`
	Confidence  int    `json:"confidence"`
}

// Box is a normalized bounding box (each field ∈ [0,1]).
type Box struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// RegionLocator resolves a click point on a full image into the bounding box +
// feature description of the object under that point, using a vision model. It
// reuses the same dual transport the Analyzer/SubjectDetector use: a Gemini-
// native inline path (no COS) and an OpenAI-compatible image_url path (needs a
// public URL). The concrete analyzer is passed in so wiring stays in main.
type RegionLocator interface {
	// LocateAndDescribe sends the FULL image plus the click point and returns the
	// object's box + description. img is whichever the analyzer needs: inline
	// Data/Mime for Gemini, or a public URL for the OpenAI path.
	LocateAndDescribe(ctx context.Context, img Image, px, py float64) (RegionResult, error)
}

// geminiAnalyzer and openAIAnalyzer both implement LocateAndDescribe below by
// reusing their existing inline / image_url request cores with a JSON-forced
// prompt, then parsing the structured RegionResult out of the reply.

// LocateAndDescribe (Gemini inline) sends the full image + click point and parses
// the structured region result. ResponseMimeType=application/json forces a bare
// JSON object.
func (a *geminiAnalyzer) LocateAndDescribe(ctx context.Context, img Image, px, py float64) (RegionResult, error) {
	if a == nil {
		return RegionResult{}, fmt.Errorf("region: analyzer not configured")
	}
	prompt := fmt.Sprintf(pointRegionPromptTmpl, px, py)
	start := time.Now()
	content, err := a.generateJSON(ctx, img, prompt)
	if err != nil {
		return RegionResult{}, err
	}
	res, err := parseRegion(content)
	if err != nil {
		applog.From(ctx).Warn().Str("event", "region.parse_failed").
			Str("model", a.model).Err(err).Str("raw", truncate(content, 300)).
			Msg("region result unparseable")
		return RegionResult{}, err
	}
	applog.From(ctx).Info().Str("event", "region.locate").
		Str("model", a.model).Dur("dur", time.Since(start)).
		Float64("box_x", res.Box.X).Float64("box_y", res.Box.Y).
		Float64("box_w", res.Box.W).Float64("box_h", res.Box.H).
		Int("confidence", res.Confidence).Msg("region located")
	return res, nil
}

// LocateAndDescribe (OpenAI-compatible) mirrors the Gemini path over the
// image_url transport (needs a public URL in img.URL).
func (a *openAIAnalyzer) LocateAndDescribe(ctx context.Context, img Image, px, py float64) (RegionResult, error) {
	if a == nil {
		return RegionResult{}, fmt.Errorf("region: analyzer not configured")
	}
	prompt := fmt.Sprintf(pointRegionPromptTmpl, px, py)
	start := time.Now()
	content, err := a.analyzeWithPrompt(ctx, []Image{img}, prompt, nil)
	if err != nil {
		return RegionResult{}, err
	}
	res, err := parseRegion(content)
	if err != nil {
		applog.From(ctx).Warn().Str("event", "region.parse_failed").
			Str("model", a.model).Err(err).Str("raw", truncate(content, 300)).
			Msg("region result unparseable")
		return RegionResult{}, err
	}
	applog.From(ctx).Info().Str("event", "region.locate").
		Str("model", a.model).Dur("dur", time.Since(start)).
		Int("confidence", res.Confidence).Msg("region located")
	return res, nil
}

// parseRegion extracts the JSON region object from model output (tolerating
// prose/fences via extractJSON), clamps the box into [0,1], and caps the
// description length. An empty description or a degenerate box is an error so the
// caller degrades to plain-text editing.
func parseRegion(content string) (RegionResult, error) {
	js := extractJSON(content)
	if js == "" {
		return RegionResult{}, fmt.Errorf("region: no JSON in output")
	}
	var res RegionResult
	if err := json.Unmarshal([]byte(js), &res); err != nil {
		return RegionResult{}, fmt.Errorf("region: parse result: %w", err)
	}
	res.Box.X = clamp01(res.Box.X)
	res.Box.Y = clamp01(res.Box.Y)
	res.Box.W = clamp01(res.Box.W)
	res.Box.H = clamp01(res.Box.H)
	// Clamp box so it never exceeds the frame (x+w ≤ 1).
	if res.Box.X+res.Box.W > 1 {
		res.Box.W = 1 - res.Box.X
	}
	if res.Box.Y+res.Box.H > 1 {
		res.Box.H = 1 - res.Box.Y
	}
	if len([]rune(res.Description)) > maxReportLen {
		res.Description = string([]rune(res.Description)[:maxReportLen])
	}
	if res.Description == "" {
		return RegionResult{}, fmt.Errorf("region: empty description")
	}
	return res, nil
}
