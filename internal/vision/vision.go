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
)

// analysisPrompt is a fixed server-side instruction. Never mixed with user text.
// Kept intentionally concise so the output is directly usable as a generation
// prompt constraint — focus on what matters for faithful adaptation, skip
// general background description.
const analysisPrompt = `分析以下游戏宣发图，按固定格式输出，不要输出任何其他内容：

主体：[角色外貌、服装、标志性特征，一句话]
宣发意图：[核心宣传主题/活动/卖点，一句话]
必须保留：[适配各尺寸时绝不可缺少的视觉元素，分号分隔]
配色：[主色调，3个以内，用 hex 或颜色名]`

// maxReportLen caps the report before injecting into the generation prompt.
const maxReportLen = 400

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
		"model":      a.model,
		"stream":     true,
		"max_tokens": 200, // fixed-format output is short; cap prevents runaway loops
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
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			text := chunk.Choices[0].Delta.Content
			if text != "" {
				full.WriteString(text)
				// Stop streaming to the chat once we hit the display cap; the
				// scanner keeps draining the body (so the HTTP conn stays clean)
				// but we don't push more chunks to the frontend.
				if full.Len() <= maxReportLen && onChunk != nil {
					onChunk(text)
				}
				// Hard-stop accumulation beyond the cap to prevent OOM on loops.
				if full.Len() >= maxReportLen*2 {
					break
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return full.String(), fmt.Errorf("vision: read stream: %w", err)
	}
	report := full.String()
	if len(report) > maxReportLen {
		report = report[:maxReportLen]
	}
	log.Printf("vision.analyze: done in %s report_len=%d", time.Since(start), len(report))
	return report, nil
}
