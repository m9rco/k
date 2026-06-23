package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	applog "gameasset/internal/log"
)

// subjectPrompt is the fixed instruction for the subject-locator vision call. It
// asks the model to report WHERE the main marketing subject sits in the frame —
// not to judge quality — so an extreme-ratio cover crop can keep that point in
// frame instead of blindly center-cropping (which decapitates an off-center
// subject on a 5:1/6:1 banner). The model returns ONLY strict JSON; the server
// uses the normalized center and a confidence score, falling back to a center
// crop when confidence is low or the output is unparseable.
const subjectPrompt = `你是图像构图分析器。下面给你一张【宣发素材图】。请定位画面中“最重要的主体”的位置——主体指：核心角色/人物（优先其头部与面部）、品牌 LOGO、以及主标题文案，三者构成的视觉重心。请只输出一个 JSON 对象，不要输出任何解释、前后缀或 Markdown。

坐标系：以图片左上角为原点 (0,0)，右下角为 (1,1)。center_x / center_y 是上述视觉重心的归一化中心坐标，范围 [0,1]。
confidence：你对该定位的把握程度，0-100；画面无明确主体、或主体铺满整幅、或无法判断时给低分（<50）。

只输出如下 JSON：
{"center_x":0.5,"center_y":0.5,"confidence":0}`

// SubjectBox is the parsed result of a subject-locator call: the main subject's
// normalized center plus a confidence score. CenterX/CenterY ∈ [0,1] with the
// origin at the image's top-left. Confidence ∈ [0,100]; the caller ignores a
// detection below its own threshold and falls back to a center crop.
type SubjectBox struct {
	CenterX    float64
	CenterY    float64
	Confidence int
}

// SubjectDetector locates the main marketing subject in a product image via a
// vision-capable model, so extreme-ratio adaptation can crop toward the subject
// rather than the geometric center. Transport mirrors QualityChecker: Gemini
// models use the native generateContent API with inline bytes + JSON mime;
// everything else uses the OpenAI-compatible image_url path. The image is sent
// inline in both, so no public URL / COS is required.
type SubjectDetector struct {
	baseURL  string
	apiKey   string
	model    string
	isGemini bool
	client   *http.Client

	// wantMasks gates the segmentation-mask request path. OFF by default: a plain
	// box-only detection returns in ~20s, while asking Gemini for per-subject masks
	// over the yunwu gateway either hangs past the timeout (gemini-2.5-pro) or comes
	// back with literal "..." placeholders instead of base64 PNGs (flash) — i.e. the
	// gateway does not actually transmit masks. Turn ON (SetWantMasks) only against a
	// backend verified to return real base64 mask data, so a "frozen" split can't be
	// reintroduced silently.
	wantMasks bool
}

// NewSubjectDetector returns a detector, or nil when baseURL/apiKey is empty
// (caller treats nil as "disabled" → extreme-ratio crops stay center-anchored).
// The transport is auto-selected from the model name, matching NewQualityChecker.
func NewSubjectDetector(baseURL, apiKey, model string) *SubjectDetector {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" {
		return nil
	}
	if model == "" {
		model = "doubao-seed-1-6-vision-250815"
	}
	isGemini := strings.Contains(strings.ToLower(model), "gemini")
	base := strings.TrimRight(baseURL, "/")
	if isGemini {
		base = strings.TrimSuffix(base, "/v1beta")
		base = strings.TrimSuffix(base, "/v1")
		if base == "" {
			base = "https://generativelanguage.googleapis.com"
		}
	}
	return &SubjectDetector{
		baseURL:  base,
		apiKey:   apiKey,
		model:    model,
		isGemini: isGemini,
		client:   &http.Client{Timeout: 180 * time.Second},
	}
}

// Configured reports whether the detector is ready to use.
func (d *SubjectDetector) Configured() bool { return d != nil }

// SetWantMasks enables the segmentation-mask request path (Gemini transport only).
// Default is OFF: see SubjectDetector.wantMasks. Enable only against a backend
// verified to return real base64 mask data — otherwise the split hangs or yields
// placeholder masks. No-op on a nil detector or a non-Gemini transport.
func (d *SubjectDetector) SetWantMasks(v bool) {
	if d == nil {
		return
	}
	d.wantMasks = v && d.isGemini
}

// rawSubject is the strict JSON the locator model is asked to emit.
type rawSubject struct {
	CenterX    float64 `json:"center_x"`
	CenterY    float64 `json:"center_y"`
	Confidence int     `json:"confidence"`
}

// Detect locates the main subject in the product image. img is the raw bytes,
// mime its content type. It returns the parsed box. On any error (network,
// timeout, unparseable output) it returns a zero box with the error so the
// caller degrades to a center crop and never blocks the adapt pipeline.
func (d *SubjectDetector) Detect(ctx context.Context, img []byte, mime string) (SubjectBox, error) {
	if d == nil {
		return SubjectBox{}, fmt.Errorf("subject: detector not configured")
	}
	if len(img) == 0 {
		return SubjectBox{}, fmt.Errorf("subject: no image bytes")
	}

	start := time.Now()
	var content string
	var err error
	if d.isGemini {
		content, err = d.askGemini(ctx, subjectPrompt, img, mime)
	} else {
		content, err = d.askOpenAI(ctx, subjectPrompt, img, mime)
	}
	if err != nil {
		return SubjectBox{}, err
	}

	box, err := parseSubject(content)
	if err != nil {
		applog.From(ctx).Warn().Str("event", "subject.parse_failed").
			Str("model", d.model).Err(err).
			Str("raw", truncate(content, 300)).Msg("subject box unparseable; caller will center-crop")
		return SubjectBox{}, err
	}
	applog.From(ctx).Info().Str("event", "subject.detect").
		Str("model", d.model).Dur("dur", time.Since(start)).
		Float64("center_x", box.CenterX).Float64("center_y", box.CenterY).
		Int("confidence", box.Confidence).Msg("subject located")
	return box, nil
}

// askOpenAI runs the locator over an OpenAI-compatible /chat/completions endpoint,
// sending the image inline as a data URI. Returns the raw assistant message.
func (d *SubjectDetector) askOpenAI(ctx context.Context, prompt string, img []byte, mime string) (string, error) {
	dataURI := "data:" + mimeOrPNG(mime) + ";base64," + base64.StdEncoding.EncodeToString(img)

	type imgURL struct {
		URL string `json:"url"`
	}
	type contentPart struct {
		Type     string  `json:"type"`
		Text     string  `json:"text,omitempty"`
		ImageURL *imgURL `json:"image_url,omitempty"`
	}
	parts := []contentPart{
		{Type: "text", Text: prompt},
		{Type: "image_url", ImageURL: &imgURL{URL: dataURI}},
	}
	payload := map[string]any{
		"model":           d.model,
		"max_tokens":      120,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]any{
			{"role": "system", "content": "你只输出严格的 JSON 对象，不输出任何解释、前缀、后缀或 Markdown 代码块。"},
			{"role": "user", "content": parts},
		},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("subject: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("subject: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.apiKey)

	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("subject: request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("subject: status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("subject: decode envelope: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("subject: empty choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

// askGemini runs the locator over Google's native generateContent API with inline
// image bytes and responseMimeType=application/json. Mirrors QualityChecker.askGemini.
func (d *SubjectDetector) askGemini(ctx context.Context, prompt string, img []byte, mime string) (string, error) {
	body := geminiRequest{
		Contents: []geminiContent{{Parts: []geminiPart{
			{Text: prompt},
			{InlineData: &geminiInlineData{
				MimeType: mimeOrPNG(mime),
				Data:     base64.StdEncoding.EncodeToString(img),
			}},
		}}},
		GenerationConfig: &geminiGenConfig{ResponseMimeType: "application/json"},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("subject: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", d.baseURL, d.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("subject: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.apiKey)
	req.Header.Set("x-goog-api-key", d.apiKey)

	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("subject: request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("subject: status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []geminiPart `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("subject: decode envelope: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("subject: api error: %s", parsed.Error.Message)
	}
	var out strings.Builder
	for _, c := range parsed.Candidates {
		for _, p := range c.Content.Parts {
			if p.Text != "" {
				out.WriteString(p.Text)
			}
		}
	}
	return out.String(), nil
}

// parseSubject extracts the JSON box from the model output and clamps the center
// into [0,1]. Reuses extractJSON for prose/fence tolerance.
func parseSubject(content string) (SubjectBox, error) {
	js := extractJSON(content)
	if js == "" {
		return SubjectBox{}, fmt.Errorf("subject: no JSON in output")
	}
	var rs rawSubject
	if err := json.Unmarshal([]byte(js), &rs); err != nil {
		return SubjectBox{}, fmt.Errorf("subject: parse box: %w", err)
	}
	box := SubjectBox{CenterX: clamp01(rs.CenterX), CenterY: clamp01(rs.CenterY), Confidence: rs.Confidence}
	return box, nil
}

// clamp01 constrains v to [0,1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// subjectsPrompt asks the model to enumerate the distinct FOREGROUND subjects in
// a marketing image so each can be cut into its own layer for manual fine-tuning.
// Only TWO kinds are separable: every character/person, and every marketing-copy
// block (title / selling point / campaign text). The brand LOGO, the hero
// object/prop, and the original scenery are deliberately KEPT in the background
// (the background layer is just the original image), so the layer count stays
// small and matches the "I only want to nudge people and copy" intent. It returns
// a strict JSON object; nothing else.
const subjectsPrompt = `你是图像图层分析器。下面给你一张【宣发素材图】。请列出画面中可以各自独立抠成一层、供人工微调的【前景主体】——只包括以下两类：
① 每个角色/人物；
② 每个宣发文案块（标题/卖点/活动文案）。

【重要】不要把以下内容列为主体（它们要保留在背景层里）：品牌 LOGO、画面里的核心物件/道具/商品、原始背景/场景、天空、地面、氛围、光效、装饰底纹。只分「人物」和「宣发文案」两类，不要分出过多碎片元素。

坐标系：以图片左上角为原点 (0,0)、右下角为 (1,1)。每个主体给出：
- desc：对该主体的简短中文描述（10~30字，足以让人/模型唯一指认它，如“画面左侧穿红色铠甲的男性战士”或“顶部金色描边主标题文案”）
- box：紧贴该主体的归一化包围盒 {x,y,w,h}，均 ∈ [0,1]，尽量贴合主体轮廓但不要裁掉其边缘

最多列出 5 个，按视觉重要性从高到低排序；没有可独立分层的前景主体时返回空数组。只输出 JSON 对象，不要任何解释、前后缀或 Markdown：
{"subjects":[{"desc":"...","box":{"x":0.0,"y":0.0,"w":0.0,"h":0.0}}]}`

// subjectMasksPrompt is the Gemini-only variant that ALSO asks for a per-subject
// segmentation mask, so each subject can be cut onto a transparent background by
// applying the mask as alpha over the original pixels (真抠图, no repaint). It uses
// Gemini's native segmentation output (box_2d in 0–1000 + a base64 PNG mask sized
// to that box). Same two subject kinds and cap as subjectsPrompt.
const subjectMasksPrompt = `你是图像分割器。下面给你一张【宣发素材图】。请把画面中可以各自独立抠成一层、供人工微调的【前景主体】分割出来——只包括以下两类：
① 每个角色/人物；
② 每个宣发文案块（标题/卖点/活动文案）。

【重要】不要分割以下内容（它们要保留在背景层里）：品牌 LOGO、画面里的核心物件/道具/商品、原始背景/场景、天空、地面、氛围、光效、装饰底纹。只分「人物」和「宣发文案」两类，不要分出过多碎片元素。

为每个主体输出：
- desc：该主体的简短中文描述（10~30字，足以唯一指认它）
- box_2d：该主体的边界框，格式 [ymin, xmin, ymax, xmax]，坐标归一化到 0–1000（左上角为原点）
- mask：该主体的分割掩码，base64 编码的 PNG（"data:image/png;base64,..."），尺寸与 box_2d 对齐，前景为高亮、背景为黑

最多 5 个，按视觉重要性从高到低排序；没有可独立分层的前景主体时返回空数组。只输出 JSON 对象，不要任何解释、前后缀或 Markdown：
{"subjects":[{"desc":"...","box_2d":[0,0,0,0],"mask":"data:image/png;base64,..."}]}`

// Subject is one detected foreground subject: a description, its normalized
// bounding box, and (when the detector is segmentation-capable) a Mask. Mask is
// the raw bytes of a grayscale PNG probability map sized to Box — pixel intensity
// is the foreground confidence (0=background, 255=subject). The caller applies it
// as the alpha channel over the box's VERBATIM original pixels to cut the subject
// onto a transparent background WITHOUT repainting (so the layer stays pixel-exact
// and drops back in perfect register). Mask is nil when the detector can't return
// one (non-Gemini transport, or the model omitted it) — the caller then falls
// back to an opaque rectangular crop.
type Subject struct {
	Desc string `json:"desc"`
	Box  Box    `json:"box"`
	Mask []byte `json:"-"`
}

// rawSubjectList is the strict JSON the splitter model is asked to emit.
type rawSubjectList struct {
	Subjects []struct {
		Desc string `json:"desc"`
		Box  struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
			W float64 `json:"w"`
			H float64 `json:"h"`
		} `json:"box"`
	} `json:"subjects"`
}

// maxDetectedSubjects bounds how many layers one split produces. Kept small so a
// split yields a handful of fine-tunable subject layers (people + copy) rather
// than a swarm of fragment layers.
const maxDetectedSubjects = 5

// DetectSubjects enumerates the distinct foreground subjects in the image so each
// can be cut into its own layer. Returns an ordered list (most important first),
// or an empty list when none are clearly separable. Any transport/parse error is
// returned so the caller can decide whether to proceed with a background-only split.
func (d *SubjectDetector) DetectSubjects(ctx context.Context, img []byte, mime string) ([]Subject, error) {
	if d == nil {
		return nil, fmt.Errorf("subject: detector not configured")
	}
	if len(img) == 0 {
		return nil, fmt.Errorf("subject: no image bytes")
	}
	start := time.Now()
	var content string
	var err error
	// Only ask for segmentation masks when explicitly enabled AND on a Gemini
	// transport. Otherwise use the box-only prompt (fast, reliable) and let the
	// caller fall back to an opaque rectangular crop. See SubjectDetector.wantMasks
	// for why masks are off by default over the current gateway.
	useMasks := d.wantMasks && d.isGemini
	if useMasks {
		content, err = d.askGemini(ctx, subjectMasksPrompt, img, mime)
	} else if d.isGemini {
		content, err = d.askGemini(ctx, subjectsPrompt, img, mime)
	} else {
		content, err = d.askOpenAI(ctx, subjectsPrompt, img, mime)
	}
	if err != nil {
		return nil, err
	}
	// Diagnostic: how big is the raw model output and did it even contain mask
	// fields? Distinguishes "model returned no masks" from "parser dropped them".
	applog.From(ctx).Debug().Str("event", "subjects.raw").
		Str("model", d.model).Bool("gemini", d.isGemini).Bool("want_masks", useMasks).
		Int("raw_bytes", len(content)).
		Bool("has_mask_field", strings.Contains(content, "\"mask\"")).
		Bool("has_box2d_field", strings.Contains(content, "box_2d")).
		Str("head", truncate(content, 200)).
		Msg("主体检测原始返回")
	var subs []Subject
	if useMasks {
		subs, err = parseSubjectMasks(content)
	} else {
		subs, err = parseSubjects(content)
	}
	if err != nil {
		applog.From(ctx).Warn().Str("event", "subjects.parse_failed").
			Str("model", d.model).Err(err).Str("raw", truncate(content, 400)).
			Msg("subject list unparseable")
		return nil, err
	}
	masked := 0
	for _, s := range subs {
		if len(s.Mask) > 0 {
			masked++
		}
	}
	applog.From(ctx).Info().Str("event", "subjects.detect").
		Str("model", d.model).Dur("dur", time.Since(start)).Int("count", len(subs)).
		Int("masked", masked).Msg("foreground subjects detected")
	return subs, nil
}

// parseSubjects extracts the JSON array of subjects, clamps boxes into [0,1],
// drops entries with an empty description, and caps the count.
func parseSubjects(content string) ([]Subject, error) {
	js := extractJSON(content)
	if js == "" {
		return nil, fmt.Errorf("subject: no JSON in output")
	}
	var rl rawSubjectList
	if err := json.Unmarshal([]byte(js), &rl); err != nil {
		return nil, fmt.Errorf("subject: parse list: %w", err)
	}
	out := make([]Subject, 0, len(rl.Subjects))
	for _, r := range rl.Subjects {
		desc := strings.TrimSpace(r.Desc)
		if desc == "" {
			continue
		}
		out = append(out, Subject{
			Desc: desc,
			Box:  Box{X: clamp01(r.Box.X), Y: clamp01(r.Box.Y), W: clamp01(r.Box.W), H: clamp01(r.Box.H)},
		})
		if len(out) >= maxDetectedSubjects {
			break
		}
	}
	return out, nil
}

// rawSubjectMaskList is the strict JSON the segmentation model is asked to emit:
// each subject carries a desc, a box_2d ([ymin,xmin,ymax,xmax] in 0..1000), and a
// base64 PNG mask sized to that box.
type rawSubjectMaskList struct {
	Subjects []struct {
		Desc  string    `json:"desc"`
		Label string    `json:"label"`
		Box2D []float64 `json:"box_2d"`
		Mask  string    `json:"mask"`
	} `json:"subjects"`
}

// parseSubjectMasks parses the segmentation response: it converts box_2d
// (0..1000, [ymin,xmin,ymax,xmax]) into a normalized Box, decodes each mask data
// URI into raw PNG bytes, drops entries with an empty desc or degenerate box, and
// caps the count. A subject whose mask is missing/undecodable is still kept
// (Mask=nil) so the caller falls back to an opaque crop for it rather than
// dropping the subject entirely.
func parseSubjectMasks(content string) ([]Subject, error) {
	js := extractJSON(content)
	if js == "" {
		return nil, fmt.Errorf("subject: no JSON in output")
	}
	var rl rawSubjectMaskList
	if err := json.Unmarshal([]byte(js), &rl); err != nil {
		return nil, fmt.Errorf("subject: parse mask list: %w", err)
	}
	out := make([]Subject, 0, len(rl.Subjects))
	for _, r := range rl.Subjects {
		desc := strings.TrimSpace(r.Desc)
		if desc == "" {
			desc = strings.TrimSpace(r.Label)
		}
		if desc == "" || len(r.Box2D) != 4 {
			continue
		}
		box := box2DToBox(r.Box2D)
		if box.W <= 0 || box.H <= 0 {
			continue
		}
		out = append(out, Subject{Desc: desc, Box: box, Mask: decodeMaskDataURI(r.Mask)})
		if len(out) >= maxDetectedSubjects {
			break
		}
	}
	return out, nil
}

// box2DToBox converts Gemini's [ymin,xmin,ymax,xmax] (0..1000) into a normalized
// {x,y,w,h} box clamped to [0,1], tolerating swapped min/max.
func box2DToBox(b []float64) Box {
	ymin, xmin, ymax, xmax := b[0]/1000, b[1]/1000, b[2]/1000, b[3]/1000
	if xmax < xmin {
		xmin, xmax = xmax, xmin
	}
	if ymax < ymin {
		ymin, ymax = ymax, ymin
	}
	x, y := clamp01(xmin), clamp01(ymin)
	return Box{X: x, Y: y, W: clamp01(xmax) - x, H: clamp01(ymax) - y}
}

// decodeMaskDataURI strips an optional "data:...;base64," prefix and decodes the
// remainder into raw bytes (the mask PNG). Returns nil on any error so the caller
// degrades to an opaque crop rather than failing the whole subject.
func decodeMaskDataURI(s string) []byte {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if i := strings.Index(s, "base64,"); i >= 0 {
		s = s[i+len("base64,"):]
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	return data
}
