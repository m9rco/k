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

// maxReportLen caps the report before injecting into the generation prompt.
// Aligned with generation.maxSlotLen (500), the ceiling the THEME segment is
// Sanitized to downstream: a smaller value here would let the frontend show the
// user less than what actually drives the adaptation. The fixed 4-line format
// stays well under this; the cap only guards a runaway model.
const maxReportLen = 500

// Analyzer calls a vision-capable OpenAI-compatible model to produce a
// structured marketing analysis report.
type Analyzer struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// New returns an Analyzer. Returns nil when baseURL or apiKey is empty
// (caller should treat nil as "not configured").
func New(baseURL, apiKey, model string) *Analyzer {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" {
		return nil
	}
	if model == "" {
		model = "grok-4-fast"
	}
	return &Analyzer{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// Configured reports whether the analyzer is ready to use.
func (a *Analyzer) Configured() bool { return a != nil }

// Analyze sends imageURLs to the vision model with the fixed analysis prompt and
// streams the response. onChunk is called with each text delta (may be called
// many times); the full report text is returned when streaming is complete.
// Graceful on network errors: returns ("", err) so callers can degrade cleanly.
func (a *Analyzer) Analyze(ctx context.Context, imageURLs []string, onChunk func(string)) (string, error) {
	if a == nil || len(imageURLs) == 0 {
		return "", fmt.Errorf("vision: analyzer not configured or no images")
	}

	// Build multimodal user message: text instruction + one image_url part per URL.
	type imgURL struct {
		URL string `json:"url"`
	}
	type contentPart struct {
		Type     string  `json:"type"`
		Text     string  `json:"text,omitempty"`
		ImageURL *imgURL `json:"image_url,omitempty"`
	}
	parts := make([]contentPart, 0, len(imageURLs)+1)
	parts = append(parts, contentPart{Type: "text", Text: analysisPrompt})
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
