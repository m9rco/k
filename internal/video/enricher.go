package video

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// llmEnricher enriches motion descriptions using an OpenAI-compatible LLM.
type llmEnricher struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

const enrichPromptSystem = `你是游戏宣发视频运镜与动效描述专家。将用户的简短动作描述扩写为 2-3 句专业的图生视频 prompt（英文），涵盖：主体动作、镜头运动、节奏感、光影变化。不虚构游戏中不存在的元素。若提供宣发主题，确保视频内容紧扣该主题。只输出 prompt 文本，不要任何解释或前缀。`

// NewLLMEnricher returns a prompt enricher backed by an OpenAI-compatible chat API.
// Returns nil when baseURL or apiKey is empty (caller treats nil enricher as disabled).
func NewLLMEnricher(baseURL, apiKey, model string) PromptEnricher {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" {
		return nil
	}
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	return &llmEnricher{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (e *llmEnricher) Enrich(ctx context.Context, motion, themeReport string) (string, error) {
	userText := motion
	if themeReport != "" {
		userText = "宣发主题：" + themeReport + "\n动作描述：" + motion
	}
	payload := map[string]any{
		"model":      e.model,
		"max_tokens": 200,
		"messages": []map[string]string{
			{"role": "system", "content": enrichPromptSystem},
			{"role": "user", "content": userText},
		},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("enricher: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("enricher: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("enricher: do: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("enricher: status %d", resp.StatusCode)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Choices) == 0 {
		return "", fmt.Errorf("enricher: parse response")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}
