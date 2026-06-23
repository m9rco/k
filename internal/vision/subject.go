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
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

// Configured reports whether the detector is ready to use.
func (d *SubjectDetector) Configured() bool { return d != nil }

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
// a marketing image so each can be cut into its own layer for free recompositing.
// The brand LOGO and the original scenery are deliberately KEPT in the background
// (they become part of the inpainted base layer), so the separable layers are the
// characters/people, the core hero subject(s)/key props, and the marketing copy
// blocks. It returns a strict JSON object; nothing else.
const subjectsPrompt = `你是图像图层分析器。下面给你一张【宣发素材图】。请列出画面中可以各自独立抠成一层的【前景主体】——只包括：每个角色/人物、画面核心主体或显著道具/物件、宣发文案块（标题/卖点/活动文案）。

【重要】不要把以下内容列为主体（它们要保留在背景层里）：品牌 LOGO、原始背景/场景、天空、地面、氛围、光效、装饰底纹。LOGO 属于背景，不要单独抠出。

坐标系：以图片左上角为原点 (0,0)、右下角为 (1,1)。每个主体给出：
- desc：对该主体的简短中文描述（10~30字，足以让人/模型唯一指认它，如“画面左侧穿红色铠甲的男性战士”或“顶部金色描边主标题文案”）
- box：归一化包围盒 {x,y,w,h}，均 ∈ [0,1]

最多列出 8 个，按视觉重要性从高到低排序；没有可独立分层的前景主体时返回空数组。只输出 JSON 对象，不要任何解释、前后缀或 Markdown：
{"subjects":[{"desc":"...","box":{"x":0.0,"y":0.0,"w":0.0,"h":0.0}}]}`

// Subject is one detected foreground subject: a description (used as the cutout
// region_desc) and its normalized bounding box.
type Subject struct {
	Desc string `json:"desc"`
	Box  Box    `json:"box"`
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

// maxDetectedSubjects bounds how many layers one split produces, capping the
// number of downstream cutout generations (cost/latency) regardless of model output.
const maxDetectedSubjects = 8

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
	if d.isGemini {
		content, err = d.askGemini(ctx, subjectsPrompt, img, mime)
	} else {
		content, err = d.askOpenAI(ctx, subjectsPrompt, img, mime)
	}
	if err != nil {
		return nil, err
	}
	subs, err := parseSubjects(content)
	if err != nil {
		applog.From(ctx).Warn().Str("event", "subjects.parse_failed").
			Str("model", d.model).Err(err).Str("raw", truncate(content, 400)).
			Msg("subject list unparseable")
		return nil, err
	}
	applog.From(ctx).Info().Str("event", "subjects.detect").
		Str("model", d.model).Dur("dur", time.Since(start)).Int("count", len(subs)).
		Msg("foreground subjects detected")
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
