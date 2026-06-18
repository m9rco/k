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

// qualityPrompt is the fixed server-side judge instruction for the
// platform-adaptation quality gate. It is never mixed with user text. The judge
// scores a single adapted product against the analyzed marketing theme and
// returns ONLY a strict JSON object (no prose), which the server parses and
// turns into a pass/fail decision — the model's own wording is never trusted as
// the verdict (the server applies the compliance red line + score threshold).
const qualityPrompt = `你是游戏宣发素材质检员。下面给你一张【适配产物图】，以及它应当遵循的【宣发主题约束】与【目标规格】。请严格评估这张图并只输出一个 JSON 对象，不要输出任何解释、前后缀或 Markdown。

评估维度：
1. compliance（合规红线）：是否含违禁/敏感/侵权内容（露骨、暴恐、政治敏感、第三方商标误用等）。一旦命中即 pass=false。
2. subject_consistency（主体一致性，0-100）：宣发主体/角色是否与主题约束一致、未被偷换。
3. character_appeal（人物卖相，0-100）：主体是否显眼、构图突出、未被裁切到边角或糊化。
4. overall_quality（整体质量，0-100）：清晰度、构图、和谐度。
5. canvas_fill（画面完整度，0-100）：画面是否完整填充目标尺寸，无明显白边、纯色色块、透明带或未填充留白区域。存在任何明显留白即严重扣分（≤40分）。
6. key_elements_fidelity（必备要素保真，0-100）：对照【宣发主题约束】中「必须保留」清单，检查：(a) 核心角色/主体是否在画面内；(b) 游戏 LOGO 是否可见；(c) 要求保留的文字是否存在且字符正确（未糊化、未改写、未变成乱码）。**按【目标规格】文案约定过滤**：若规格含「无文案」，则纯文案类要素（定档大字、底部标签等）不计入评分，只考察主体与 LOGO；若含「仅 logo」，同理只考察主体与 LOGO。主体或 LOGO 缺失、或要求保留的文字被改写/糊化时严重扣分（≤30分）。
7. ad_appeal（宣发吸引力，0-100）：该素材在信息流中的视觉冲击力——主体是否醒目占据视觉重心、色彩对比是否鲜明有层次、构图是否符合广告投放惯例（三分法/黄金分割）、整体是否达到「投放级」质感。此维度仅供参考，不计入合规或评分红线。

8. fault_source（缺陷来源，仅在不及格时有意义）："repaint" = 问题出在 AI 重绘主体/内容（主体错误、整体模糊、构图失当）；"outpaint" = 问题出在场景延伸填充（边界割裂、填充与主体风格不一致、边缘留白/色块）；"both" = 两处均有明显问题。若未经 outpaint（纯比例缩放产物），一律填 "repaint"。

生成 hints 时须遵守：若【目标规格】含「无文案」，hints 不得建议补充定档大字、底部标签等纯文案要素；若含「仅 logo」，hints 只可提 LOGO，不建议补充其他文案。

只输出如下 JSON：
{"compliance":{"pass":true,"violations":[]},"scores":{"subject_consistency":0,"character_appeal":0,"overall_quality":0,"canvas_fill":0,"key_elements_fidelity":0,"ad_appeal":0},"total":0,"fault_source":"repaint","hints":"若需改进，一句话说明重绘时应强化什么；无需改进则留空"}
其中 total 为五个分数的综合（0-100）。hints 必须是可直接追加到图生图提示词的中文要点。`

// QualityVerdict is the parsed, server-evaluated result of a quality check.
type QualityVerdict struct {
	// Pass is the final decision after applying the compliance red line and the
	// score threshold. A nil/failed check degrades to Pass=true upstream.
	Pass bool
	// Total is the model's weighted total score (0-100), for logging/telemetry.
	Total int
	// Compliant is false when the compliance red line was hit (forces fail).
	Compliant bool
	// Reasons are short human-readable failure causes (violations + low dims).
	Reasons []string
	// Hints is the model's improvement note, injected into the regenerate prompt.
	Hints string
	// FaultSource identifies which pipeline step caused the defect: "repaint"
	// (gpt-image-2), "outpaint" (Gemini scene extension), or "both". Empty
	// degrades to "repaint". Used by service.run to pick the retry strategy.
	FaultSource string
	// DimScores holds the per-dimension scores for logging; zero when unparseable.
	DimScores struct {
		SubjectConsistency  int
		CharacterAppeal     int
		OverallQuality      int
		CanvasFill          int
		KeyElementsFidelity int
		AdAppeal            int
	}
}

// QualityChecker scores an adapted product via a vision-capable model. Two
// transports are supported, picked by model name: Gemini models (name contains
// "gemini") use Google's native generateContent API with inlineData + a strict
// responseMimeType=application/json constraint (the OpenAI-compat /chat/completions
// path's response_format:json_object is NOT honored by Gemini gateways, which
// reply with prose + a markdown fence and no JSON body); everything else uses the
// OpenAI-compatible chat/completions image_url path. The product image is sent
// inline in both, so no public URL / COS is required.
type QualityChecker struct {
	baseURL                string
	apiKey                 string
	model                  string
	threshold              int
	keyElementsFidelityMin int
	isGemini               bool
	client                 *http.Client
}

// NewQualityChecker returns a checker, or nil when baseURL/apiKey is empty
// (caller treats nil as "gate disabled" → everything passes). threshold is the
// weighted-total score at/above which a product passes (compliance is a separate
// hard red line); a non-positive threshold falls back to 75. The transport is
// auto-selected from the model name: a "gemini" model routes to the native API.
func NewQualityChecker(baseURL, apiKey, model string, threshold, keyElementsFidelityMin int) *QualityChecker {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" {
		return nil
	}
	if model == "" {
		model = "doubao-seed-1-6-vision-250815"
	}
	if threshold <= 0 {
		threshold = 75
	}
	if keyElementsFidelityMin <= 0 {
		keyElementsFidelityMin = 60
	}
	isGemini := strings.Contains(strings.ToLower(model), "gemini")
	base := strings.TrimRight(baseURL, "/")
	if isGemini {
		// Gemini native endpoint takes a bare base; strip any OpenAI-style suffix
		// so a base shared with the chat gateways still resolves to generateContent.
		base = strings.TrimSuffix(base, "/v1beta")
		base = strings.TrimSuffix(base, "/v1")
		if base == "" {
			base = "https://generativelanguage.googleapis.com"
		}
	}
	return &QualityChecker{
		baseURL:                base,
		apiKey:                 apiKey,
		model:                  model,
		threshold:              threshold,
		keyElementsFidelityMin: keyElementsFidelityMin,
		isGemini:               isGemini,
		client:                 &http.Client{Timeout: 60 * time.Second},
	}
}

// Configured reports whether the checker is ready to use.
func (q *QualityChecker) Configured() bool { return q != nil }

// rawVerdict is the strict JSON the judge model is asked to emit.
type rawVerdict struct {
	Compliance struct {
		Pass       bool     `json:"pass"`
		Violations []string `json:"violations"`
	} `json:"compliance"`
	Scores struct {
		SubjectConsistency  int `json:"subject_consistency"`
		CharacterAppeal     int `json:"character_appeal"`
		OverallQuality      int `json:"overall_quality"`
		CanvasFill          int `json:"canvas_fill"`
		KeyElementsFidelity int `json:"key_elements_fidelity"`
		AdAppeal            int `json:"ad_appeal"`
	} `json:"scores"`
	Total       int    `json:"total"`
	FaultSource string `json:"fault_source"`
	Hints       string `json:"hints"`
}

// Check scores one product image against the marketing theme report and target
// spec. img is the raw product bytes; mime its content type; themeReport the
// analyzed subject/intent truth (may be empty); specLabel a short target
// description (e.g. "TapTap 推广图 1920×1080"). It returns the server-evaluated
// verdict. On any error (network, timeout, unparseable output) it returns a
// passing verdict with the error, so the caller degrades to "pass" and never
// blocks the adapt pipeline.
func (q *QualityChecker) Check(ctx context.Context, img []byte, mime, themeReport, specLabel string) (QualityVerdict, error) {
	pass := QualityVerdict{Pass: true, Compliant: true}
	if q == nil {
		return pass, nil
	}
	if len(img) == 0 {
		return pass, fmt.Errorf("quality: no image bytes")
	}

	var ctxText strings.Builder
	ctxText.WriteString(qualityPrompt)
	if specLabel != "" {
		ctxText.WriteString("\n\n【目标规格】" + specLabel)
	}
	if themeReport != "" {
		ctxText.WriteString("\n\n【宣发主题约束】\n" + themeReport)
	}
	prompt := ctxText.String()

	start := time.Now()
	var content string
	var err error
	if q.isGemini {
		content, err = q.askGemini(ctx, prompt, img, mime)
	} else {
		content, err = q.askOpenAI(ctx, prompt, img, mime)
	}
	if err != nil {
		return pass, err
	}

	verdict, err := q.evaluate(content)
	if err != nil {
		// Log the model's raw reply so a degrade-to-pass (e.g. non-JSON output) is
		// diagnosable from the log file rather than silently swallowed.
		applog.From(ctx).Warn().Str("event", "quality.parse_failed").
			Str("model", q.model).Err(err).
			Str("raw", truncate(content, 400)).Msg("quality verdict unparseable; degrading to pass")
		return pass, err
	}
	applog.From(ctx).Info().Str("event", "quality.check").
		Str("model", q.model).Dur("dur", time.Since(start)).
		Int("total", verdict.Total).Bool("compliant", verdict.Compliant).
		Bool("pass", verdict.Pass).Int("threshold", q.threshold).
		Int("subject_consistency", verdict.DimScores.SubjectConsistency).
		Int("character_appeal", verdict.DimScores.CharacterAppeal).
		Int("overall_quality", verdict.DimScores.OverallQuality).
		Int("canvas_fill", verdict.DimScores.CanvasFill).
		Int("key_elements_fidelity", verdict.DimScores.KeyElementsFidelity).
		Int("ad_appeal", verdict.DimScores.AdAppeal).
		Msg("quality gate evaluated")
	return verdict, nil
}

// askOpenAI runs the judge over an OpenAI-compatible /chat/completions endpoint
// (doubao and the like), sending the image inline as a data URI. Returns the raw
// assistant message content.
func (q *QualityChecker) askOpenAI(ctx context.Context, prompt string, img []byte, mime string) (string, error) {
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
		"model":           q.model,
		"max_tokens":      400,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]any{
			{"role": "system", "content": "你只输出严格的 JSON 对象，不输出任何解释、前缀、后缀或 Markdown 代码块。"},
			{"role": "user", "content": parts},
		},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("quality: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("quality: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+q.apiKey)

	resp, err := q.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("quality: request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("quality: status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("quality: decode envelope: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("quality: empty choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

// askGemini runs the judge over Google's native generateContent API with inline
// image bytes and responseMimeType=application/json — the constraint Gemini
// actually honors (its OpenAI-compat gateway ignores response_format:json_object
// and replies with a prose preamble + markdown fence, no JSON). Mirrors the
// verified request shape in geminiAnalyzer. Returns the raw text part.
func (q *QualityChecker) askGemini(ctx context.Context, prompt string, img []byte, mime string) (string, error) {
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
		return "", fmt.Errorf("quality: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", q.baseURL, q.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("quality: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// yunwu proxies accept a Bearer token; the native Google API uses
	// x-goog-api-key. Send both so either proxy form authenticates.
	req.Header.Set("Authorization", "Bearer "+q.apiKey)
	req.Header.Set("x-goog-api-key", q.apiKey)

	resp, err := q.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("quality: request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("quality: status %d: %s", resp.StatusCode, truncate(string(raw), 300))
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
		return "", fmt.Errorf("quality: decode envelope: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("quality: api error: %s", parsed.Error.Message)
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

// evaluate parses the model's JSON content and applies the compliance red line
// and score threshold to produce the final verdict. The model's own wording is
// never trusted as the verdict — only its structured scores.
func (q *QualityChecker) evaluate(content string) (QualityVerdict, error) {
	js := extractJSON(content)
	if js == "" {
		return QualityVerdict{Pass: true, Compliant: true}, fmt.Errorf("quality: no JSON in output")
	}
	var rv rawVerdict
	if err := json.Unmarshal([]byte(js), &rv); err != nil {
		return QualityVerdict{Pass: true, Compliant: true}, fmt.Errorf("quality: parse verdict: %w", err)
	}
	total := rv.Total
	if total == 0 {
		// Model omitted total: derive from all four dimension scores.
		total = (rv.Scores.SubjectConsistency + rv.Scores.CharacterAppeal + rv.Scores.OverallQuality + rv.Scores.CanvasFill) / 4
	}
	v := QualityVerdict{Total: total, Compliant: rv.Compliance.Pass, Hints: strings.TrimSpace(rv.Hints), FaultSource: rv.FaultSource}
	v.DimScores.SubjectConsistency = rv.Scores.SubjectConsistency
	v.DimScores.CharacterAppeal = rv.Scores.CharacterAppeal
	v.DimScores.OverallQuality = rv.Scores.OverallQuality
	v.DimScores.CanvasFill = rv.Scores.CanvasFill
	v.DimScores.KeyElementsFidelity = rv.Scores.KeyElementsFidelity
	v.DimScores.AdAppeal = rv.Scores.AdAppeal
	// Compliance is a hard red line: a violation fails regardless of score.
	if !rv.Compliance.Pass {
		v.Pass = false
		v.Reasons = append(v.Reasons, "合规红线")
		v.Reasons = append(v.Reasons, rv.Compliance.Violations...)
		return v, nil
	}
	// Key-elements fidelity is a hard red line: missing subject/LOGO or rewritten
	// text fails regardless of the weighted total score (0 disables the check).
	if q.keyElementsFidelityMin > 0 && rv.Scores.KeyElementsFidelity < q.keyElementsFidelityMin {
		v.Pass = false
		v.Reasons = append(v.Reasons, "核心主体/LOGO 缺失或文字被改写")
		return v, nil
	}
	// Canvas fill is a hard red line at 60: obvious blank/white/transparent areas
	// are an objective defect that must trigger a regeneration regardless of total.
	const canvasFillMin = 60
	if rv.Scores.CanvasFill < canvasFillMin {
		v.Pass = false
		v.Reasons = append(v.Reasons, "画面存在明显留白或未填充区域")
		return v, nil
	}
	// Otherwise the weighted total must clear the threshold.
	if total < q.threshold {
		v.Pass = false
		if rv.Scores.SubjectConsistency < q.threshold {
			v.Reasons = append(v.Reasons, "主体一致性偏低")
		}
		if rv.Scores.CharacterAppeal < q.threshold {
			v.Reasons = append(v.Reasons, "人物卖相不足")
		}
		if rv.Scores.OverallQuality < q.threshold {
			v.Reasons = append(v.Reasons, "整体质量偏低")
		}
		if rv.Scores.CanvasFill < q.threshold {
			v.Reasons = append(v.Reasons, "画面完整度偏低")
		}
		if len(v.Reasons) == 0 {
			v.Reasons = append(v.Reasons, "综合评分不达标")
		}
		return v, nil
	}
	v.Pass = true
	// ad_appeal is advisory only (does not affect pass/fail). When low, append
	// an appeal hint so the regeneration prompt can address it.
	if rv.Scores.AdAppeal > 0 && rv.Scores.AdAppeal < 50 {
		appeal := "宣发吸引力偏低：加强主体视觉冲击力、优化色彩层次与构图动感"
		if v.Hints != "" {
			v.Hints = v.Hints + "；" + appeal
		} else {
			v.Hints = appeal
		}
	}
	return v, nil
}

// extractJSON returns the first balanced top-level {...} object in s, tolerating
// models that wrap the JSON in prose or ```json fences. Returns "" if none.
func extractJSON(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
