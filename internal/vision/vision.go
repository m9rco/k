// Package vision provides a one-shot multimodal analysis client for game
// marketing material. It calls an OpenAI-compatible /chat/completions endpoint
// with image_url content parts and streams the response as plain text chunks.
package vision

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// const analysisPrompt = `分析以下游戏宣发图，按固定格式输出，不要输出任何其他内容：

// Produce a structured report covering:
// 1. 核心主题/IP/游戏名 (Core Theme/IP/Game Name)
// 2. 主体角色与场景 (Main Characters & Scene) — exact appearance, outfit, distinctive features
// 3. 核心卖点与文案 (Key Selling Points & Copy) — any visible text, slogans
// 4. 视觉风格与基调 (Visual Style & Tone)
// 5. 主配色调 (Dominant Color Palette)
// 6. 绝不可丢失的要素 (Must-Preserve Elements for all adapted sizes) — be specific
// 7. 各尺寸适配注意点 (Adaptation Notes) — what to emphasize when reformatting to landscape/portrait/square/banner
// 主体：[角色外貌、服装、标志性特征，一句话]
// 宣发意图：[核心宣传主题/活动/卖点，一句话]
// 必须保留：[适配各尺寸时绝不可缺少的视觉元素，分号分隔]
// 配色：[主色调，3个以内，用 hex 或颜色名]`

// analysisPrompt is a fixed server-side instruction. Never mixed with user text.
// Kept intentionally concise so the output is directly usable as a generation
// prompt constraint — focus on what matters for faithful adaptation, skip
// general background description.
const analysisPrompt = `分析以下游戏宣发图，按固定格式输出，不要输出任何其他内容：

核心主题/IP/游戏名：[游戏名、IP] 
主体：[角色外貌、服装、标志性特征，一句话]
宣发意图：[核心宣传主题/活动/卖点，一句话]
必须保留：[适配各尺寸时绝不可缺少的视觉元素，分号分隔]`

// regionPrompt is the fixed server-side instruction for describing ONE
// user-selected region cropped out of an asset. Like analysisPrompt it is pure
// server text — never concatenated with user input — and it constrains the model
// to describe only what is actually inside the crop (no hallucination, no
// describing the rest of the frame). The output is a compact structured block so
// it can be dropped straight into the edit prompt's region_desc slot to pin
// "which subject/layer is being edited".
const regionPrompt = `你看到的是一张游戏宣发图里被用户框选出来的【局部区域】。只描述这个区域里确实存在的内容，不要描述框外、不要虚构、不要推测。按固定格式输出，不要输出任何其他内容：

主体：[这个区域里的主体是什么——角色/物体/文字/UI元素，类别+一句话外观]
外观：[材质、颜色、纹理、关键细节，分号分隔]
文字：[区域内可见的文字内容，没有则写「无」]
位置：[该主体在整张图中的大致相对位置，如「画面左侧」「中央偏下」]
必须保留：[改图时这个主体绝不可丢失/改变身份的关键视觉特征，分号分隔]`

// maxReportLen caps the report before injecting into the generation prompt.
// Aligned with generation.maxSlotLen (500), the ceiling the THEME segment is
// Sanitized to downstream: a smaller value here would let the frontend show the
// user less than what actually drives the adaptation. The fixed 4-line format
// stays well under this; the cap only guards a runaway model.
const maxReportLen = 500

// Image is one analysis input. An analyzer uses whichever fields it needs:
// the OpenAI-compatible path reads URL (a public https image_url); the Gemini
// native path reads Data/Mime inline (no public URL required).
type Image struct {
	URL  string // public https URL (OpenAI-compatible image_url path)
	Data []byte // raw image bytes (Gemini inline path)
	Mime string // mime for Data (e.g. image/png)
}

// Analyzer produces a structured marketing-analysis report from a set of game
// promo images. Two implementations exist: an OpenAI-compatible one (image_url,
// needs public URLs / COS) and a Gemini-native one (inline base64, no COS).
type Analyzer interface {
	// Configured reports whether the analyzer is ready to use.
	Configured() bool
	// NeedsPublicURL reports whether Analyze requires Image.URL to be set. When
	// false, the analyzer reads Image.Data/Mime inline and the caller may skip
	// publishing the image to a public URL entirely.
	NeedsPublicURL() bool
	// Analyze streams the report; onChunk is called with each text delta (may be
	// nil). Returns the full report text. Graceful on errors: returns ("", err).
	Analyze(ctx context.Context, images []Image, onChunk func(string)) (string, error)
	// DescribeRegion describes ONE region crop using the fixed region instruction
	// (regionPrompt). Same transport as Analyze; a single image is expected.
	// Graceful on errors: returns ("", err) so callers degrade to plain-text edit.
	DescribeRegion(ctx context.Context, region Image) (string, error)
	// LocateAndDescribe resolves a click point on the FULL image into the clicked
	// object's normalized bounding box + structured feature description. img is the
	// full asset (inline for Gemini, public URL for OpenAI). Graceful on errors.
	LocateAndDescribe(ctx context.Context, img Image, px, py float64) (RegionResult, error)
}

// openAIAnalyzer calls a vision-capable OpenAI-compatible model (e.g. grok-4-fast)
// to produce a structured marketing analysis report via /chat/completions with
// image_url content parts (requires public URLs).
type openAIAnalyzer struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewOpenAI returns an OpenAI-compatible analyzer (image_url path). Returns nil
// when baseURL or apiKey is empty (caller treats nil as "not configured").
func NewOpenAI(baseURL, apiKey, model string) Analyzer {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" {
		return nil
	}
	if model == "" {
		model = "grok-4-fast"
	}
	return &openAIAnalyzer{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// Configured reports whether the analyzer is ready to use.
func (a *openAIAnalyzer) Configured() bool { return a != nil }

// NeedsPublicURL is true: the image_url path requires public URLs.
func (a *openAIAnalyzer) NeedsPublicURL() bool { return true }

// Analyze sends each image's URL to the vision model with the fixed analysis
// prompt and streams the response. onChunk is called with each text delta (may
// be called many times); the full report text is returned when streaming is
// complete. Graceful on network errors: returns ("", err) so callers can
// degrade cleanly.
func (a *openAIAnalyzer) Analyze(ctx context.Context, images []Image, onChunk func(string)) (string, error) {
	return a.analyzeWithPrompt(ctx, images, analysisPrompt, onChunk)
}

// DescribeRegion runs the fixed region instruction over a single region crop.
// The OpenAI-compatible path requires a public URL (NeedsPublicURL()=true), so
// region.URL must be set by the caller; onChunk is unused (returns full text).
func (a *openAIAnalyzer) DescribeRegion(ctx context.Context, region Image) (string, error) {
	return a.analyzeWithPrompt(ctx, []Image{region}, regionPrompt, nil)
}

func (a *openAIAnalyzer) analyzeWithPrompt(ctx context.Context, images []Image, prompt string, onChunk func(string)) (string, error) {
	if a == nil || len(images) == 0 {
		return "", fmt.Errorf("vision: analyzer not configured or no images")
	}
	imageURLs := make([]string, 0, len(images))
	for _, im := range images {
		if im.URL != "" {
			imageURLs = append(imageURLs, im.URL)
		}
	}
	if len(imageURLs) == 0 {
		return "", fmt.Errorf("vision: no image URLs provided")
	}

	type imgURL struct {
		URL string `json:"url"`
	}
	type contentPart struct {
		Type     string  `json:"type"`
		Text     string  `json:"text,omitempty"`
		ImageURL *imgURL `json:"image_url,omitempty"`
	}
	parts := make([]contentPart, 0, len(imageURLs)+1)
	parts = append(parts, contentPart{Type: "text", Text: prompt})
	for _, u := range imageURLs {
		parts = append(parts, contentPart{Type: "image_url", ImageURL: &imgURL{URL: u}})
	}

	payload := map[string]any{
		"model":  a.model,
		"stream": true,
		// The fixed 4-line format is short in characters, but CJK output costs
		// ~1.5–2 tokens per glyph, so a multi-character analysis (game name + IP +
		// several subject descriptions + intent + must-keep list) needs far more
		// than the old 200-token cap — which truncated mid-line and dropped the
		// trailing 必须保留 row entirely. 800 comfortably fits the full format while
		// still bounding a runaway loop.
		"max_tokens": 800,
		"messages": []map[string]any{
			{"role": "user", "content": parts},
		},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("vision: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("vision: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

	log.Printf("vision.analyze: model=%s images=%d endpoint=%s/chat/completions",
		a.model, len(imageURLs), a.baseURL)
	for i, u := range imageURLs {
		log.Printf("vision.analyze: image[%d] url=%s", i, u)
	}

	start := time.Now()
	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vision: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("vision: status %d: %s", resp.StatusCode, body)
	}

	var full strings.Builder
	var fullRunes int       // rune count so far (CJK-correct cap; full.Len() is bytes)
	var finishReason string // captured from the stream to surface length-truncation
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			if fr := chunk.Choices[0].FinishReason; fr != "" {
				finishReason = fr
			}
			text := chunk.Choices[0].Delta.Content
			if text != "" {
				full.WriteString(text)
				fullRunes += utf8.RuneCountInString(text)
				// Stop streaming to the chat once we hit the display cap; the
				// scanner keeps draining the body (so the HTTP conn stays clean)
				// but we don't push more chunks to the frontend.
				if fullRunes <= maxReportLen && onChunk != nil {
					onChunk(text)
				}
				// Hard-stop accumulation beyond the cap to prevent OOM on loops.
				if fullRunes >= maxReportLen*2 {
					break
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return full.String(), fmt.Errorf("vision: read stream: %w", err)
	}
	report := full.String()
	// Truncate on a rune boundary so a multi-byte character (e.g. a Chinese
	// glyph) is never cut in half into an invalid "�" replacement char — which
	// would otherwise be carried verbatim into the generation prompt's THEME.
	if r := []rune(report); len(r) > maxReportLen {
		report = string(r[:maxReportLen])
	}
	// finish_reason=length means the model hit max_tokens and the report is
	// truncated mid-format (the trailing rows are missing) — log it loudly so the
	// cause is obvious rather than appearing as a silently short report.
	if finishReason == "length" {
		log.Printf("vision.analyze: WARNING output truncated by max_tokens (finish_reason=length); report may be missing trailing rows")
	}
	log.Printf("vision.analyze: done in %s report_len=%d finish_reason=%q", time.Since(start), len(report), finishReason)
	return report, nil
}
