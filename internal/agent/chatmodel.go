package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"gameasset/internal/config"
)

// chatModel is a minimal HTTP-backed implementation of Eino's
// ToolCallingChatModel. It speaks either the Anthropic Messages API or an
// OpenAI-compatible chat-completions API (DeepSeek), selected by provider.
//
// We implement the transport directly rather than pulling eino-ext so the
// binary stays light and the model layer is a thin, swappable seam (design D1).
type chatModel struct {
	cfg    config.ModelConfig
	tools  []*schema.ToolInfo
	client *http.Client
}

// newChatModel builds a ToolCallingChatModel for the given provider config.
func newChatModel(cfg config.ModelConfig) *chatModel {
	return &chatModel{
		cfg:    cfg,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// WithTools returns a copy bound to the given tool schemas (Eino contract).
func (m *chatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := *m
	cp.tools = tools
	return &cp, nil
}

func (m *chatModel) isAnthropic() bool {
	return strings.EqualFold(m.cfg.Provider, "anthropic")
}

// Generate performs a single (non-streaming) completion.
func (m *chatModel) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if m.cfg.APIKey == "" {
		return nil, fmt.Errorf("chat model %q has no API key configured", m.cfg.Provider)
	}
	if m.isAnthropic() {
		return m.generateAnthropic(ctx, input)
	}
	return m.generateOpenAI(ctx, input)
}

// Stream performs a streaming completion, emitting message deltas. For brevity
// and reliability across both providers we fetch once and chunk the result so
// downstream WS consumers still receive incremental frames.
func (m *chatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	full, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](8)
	go func() {
		defer sw.Close()
		// If the model returned tool calls, emit them as one frame.
		if len(full.ToolCalls) > 0 {
			sw.Send(full, nil)
			return
		}
		runes := []rune(full.Content)
		const chunk = 24
		for i := 0; i < len(runes); i += chunk {
			end := i + chunk
			if end > len(runes) {
				end = len(runes)
			}
			sw.Send(schema.AssistantMessage(string(runes[i:end]), nil), nil)
		}
	}()
	return sr, nil
}

func (m *chatModel) baseURL(def string) string {
	if m.cfg.BaseURL != "" {
		return strings.TrimRight(m.cfg.BaseURL, "/")
	}
	return def
}

// ---- OpenAI-compatible (DeepSeek) ----

func (m *chatModel) generateOpenAI(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
	type oaTool struct {
		Type     string         `json:"type"`
		Function map[string]any `json:"function"`
	}
	type oaMsg struct {
		Role       string           `json:"role"`
		Content    string           `json:"content"`
		ToolCalls  []map[string]any `json:"tool_calls,omitempty"`
		ToolCallID string           `json:"tool_call_id,omitempty"`
		Name       string           `json:"name,omitempty"`
	}
	msgs := make([]oaMsg, 0, len(input))
	for _, in := range input {
		om := oaMsg{Role: string(in.Role), Content: in.Content}
		if in.Role == schema.Tool {
			om.ToolCallID = in.ToolCallID
		}
		msgs = append(msgs, om)
	}
	body := map[string]any{
		"model":    m.cfg.Model,
		"messages": msgs,
	}
	if len(m.tools) > 0 {
		tools := make([]oaTool, 0, len(m.tools))
		for _, t := range m.tools {
			params, _ := toolParamsJSONSchema(t)
			tools = append(tools, oaTool{Type: "function", Function: map[string]any{
				"name":        t.Name,
				"description": t.Desc,
				"parameters":  params,
			}})
		}
		body["tools"] = tools
	}
	raw, err := m.postJSON(ctx, m.baseURL("https://api.deepseek.com/v1")+"/chat/completions", body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode openai response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai response had no choices")
	}
	c := resp.Choices[0].Message
	out := schema.AssistantMessage(c.Content, nil)
	for _, tc := range c.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, schema.ToolCall{
			ID: tc.ID,
			Function: schema.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return out, nil
}

// ---- Anthropic Messages API ----

func (m *chatModel) generateAnthropic(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
	var system string
	type block map[string]any
	type aMsg struct {
		Role    string  `json:"role"`
		Content []block `json:"content"`
	}
	msgs := make([]aMsg, 0, len(input))
	for _, in := range input {
		switch in.Role {
		case schema.System:
			if system != "" {
				system += "\n\n"
			}
			system += in.Content
		case schema.Tool:
			msgs = append(msgs, aMsg{Role: "user", Content: []block{{
				"type":        "tool_result",
				"tool_use_id": in.ToolCallID,
				"content":     in.Content,
			}}})
		case schema.Assistant:
			blocks := []block{}
			if in.Content != "" {
				blocks = append(blocks, block{"type": "text", "text": in.Content})
			}
			for _, tc := range in.ToolCalls {
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				blocks = append(blocks, block{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": args,
				})
			}
			msgs = append(msgs, aMsg{Role: "assistant", Content: blocks})
		default: // user
			msgs = append(msgs, aMsg{Role: "user", Content: []block{{"type": "text", "text": in.Content}}})
		}
	}
	body := map[string]any{
		"model":      m.cfg.Model,
		"max_tokens": 2048,
		"messages":   msgs,
	}
	if system != "" {
		body["system"] = system
	}
	if len(m.tools) > 0 {
		tools := make([]map[string]any, 0, len(m.tools))
		for _, t := range m.tools {
			params, _ := toolParamsJSONSchema(t)
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Desc,
				"input_schema": params,
			})
		}
		body["tools"] = tools
	}
	url := m.baseURL("https://api.anthropic.com") + "/v1/messages"
	raw, err := m.postJSONWithHeaders(ctx, url, body, map[string]string{
		"x-api-key":         m.cfg.APIKey,
		"anthropic-version": "2023-06-01",
	})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}
	out := schema.AssistantMessage("", nil)
	var text strings.Builder
	for _, c := range resp.Content {
		switch c.Type {
		case "text":
			text.WriteString(c.Text)
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, schema.ToolCall{
				ID: c.ID,
				Function: schema.FunctionCall{
					Name:      c.Name,
					Arguments: string(c.Input),
				},
			})
		}
	}
	out.Content = text.String()
	return out, nil
}

func (m *chatModel) postJSON(ctx context.Context, url string, body any) ([]byte, error) {
	return m.postJSONWithHeaders(ctx, url, body, map[string]string{
		"Authorization": "Bearer " + m.cfg.APIKey,
	})
}

func (m *chatModel) postJSONWithHeaders(ctx context.Context, url string, body any, headers map[string]string) ([]byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("chat provider %s returned %d: %s", m.cfg.Provider, resp.StatusCode, truncate(string(data), 300))
	}
	return data, nil
}

// drainSSE is retained for future true-streaming support; unused for now.
var _ = bufio.NewReader

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// toolParamsJSONSchema converts an Eino ToolInfo's params into a JSON-schema
// object suitable for both Anthropic and OpenAI tool definitions.
func toolParamsJSONSchema(t *schema.ToolInfo) (map[string]any, error) {
	if t.ParamsOneOf == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}, nil
	}
	js, err := t.ParamsOneOf.ToJSONSchema()
	if err != nil || js == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}, nil
	}
	b, err := json.Marshal(js)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}
