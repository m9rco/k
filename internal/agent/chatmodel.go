package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
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
	// retryBase is the base backoff unit between retries; 0 means 1s. Tests set
	// it small to keep the suite fast.
	retryBase time.Duration
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

// degradeNotifierKey scopes a per-turn callback that chatModel.Stream invokes
// when it falls back from real streaming to a re-chunked one-shot response, so
// the orchestrator can tell the frontend this turn is non-streaming.
type degradeNotifierKey struct{}

// withDegradeNotifier returns a context carrying fn, invoked (at most once per
// turn, guarded by the caller) when the chat model degrades to one-shot.
func withDegradeNotifier(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, degradeNotifierKey{}, fn)
}

// notifyDegrade invokes the context's degrade notifier if one is installed.
func notifyDegrade(ctx context.Context) {
	if fn, ok := ctx.Value(degradeNotifierKey{}).(func()); ok && fn != nil {
		fn()
	}
}

// Stream performs a streaming completion, consuming the provider's SSE stream
// so reply text and thinking surface incrementally in real time. If streaming
// setup fails before any frame is produced, it degrades to a one-shot Generate
// whose result is re-chunked, so the UI never blanks out (design D1).
func (m *chatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.cfg.APIKey == "" {
		return nil, fmt.Errorf("chat model %q has no API key configured", m.cfg.Provider)
	}
	resp, err := m.openStream(ctx, input)
	if err != nil {
		// Setup failed before any byte streamed: safe to degrade to one-shot.
		// Tell the orchestrator so it can signal the frontend a non-streaming turn.
		log.Printf("chatmodel: stream open failed, degrading to one-shot: %v", err)
		notifyDegrade(ctx)
		return m.fallbackStream(ctx, input, opts...)
	}
	sr, sw := schema.Pipe[*schema.Message](16)
	go func() {
		defer sw.Close()
		defer resp.Body.Close()
		var perr error
		if m.isAnthropic() {
			perr = streamAnthropic(resp.Body, sw)
		} else {
			perr = streamOpenAI(resp.Body, sw)
		}
		if perr != nil {
			sw.Send(nil, perr)
		}
	}()
	return sr, nil
}

// openStream issues the streaming HTTP request and returns the live response on
// a 2xx status. Non-2xx or transport errors are returned so the caller can
// decide to degrade (no frame has been emitted yet).
func (m *chatModel) openStream(ctx context.Context, input []*schema.Message) (*http.Response, error) {
	var (
		url     string
		headers map[string]string
		body    map[string]any
	)
	if m.isAnthropic() {
		url, headers, body = m.anthropicURL(), m.anthropicHeaders(), m.anthropicBody(input)
	} else {
		url, headers, body = m.openAIURL(), map[string]string{"Authorization": "Bearer " + m.cfg.APIKey}, m.openAIBody(input)
	}
	body["stream"] = true
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal stream request: %w", err)
	}
	hdr := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "text/event-stream",
	}
	for k, v := range headers {
		hdr[k] = v
	}
	// Stream open shares the retry path but with a SMALL budget: a transient
	// 429/5xx here fails over fast to the one-shot fallback (which keeps the full
	// retry budget and, on flaky proxies, often hits a healthier non-streaming
	// endpoint) instead of stalling the user ~15s on the streaming endpoint.
	resp, err := m.sendWithRetry(ctx, url, buf, hdr, streamOpenRetries)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("chat provider %s stream returned %d: %s", m.cfg.Provider, resp.StatusCode, truncate(string(data), 300))
	}
	// Some proxies accept stream:true but answer with a buffered, non-SSE JSON
	// body. Our SSE scanner would find no `data:` lines and yield empty output.
	// Detect that here and signal a degrade so the caller falls back to one-shot.
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "event-stream") {
		resp.Body.Close()
		return nil, fmt.Errorf("provider %s did not return an SSE stream (content-type %q)", m.cfg.Provider, ct)
	}
	return resp, nil
}

// fallbackStream re-chunks a one-shot Generate into stream frames so a provider
// that rejects streaming still yields incremental UI updates.
func (m *chatModel) fallbackStream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	full, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](8)
	go func() {
		defer sw.Close()
		// Re-chunk reasoning the same way as the body so the degraded path still
		// yields a typewriter effect instead of one big block (parity with the
		// streaming path's per-chunk reasoning frames).
		if full.ReasoningContent != "" {
			for _, frag := range chunkRunes(full.ReasoningContent, 24) {
				rm := schema.AssistantMessage("", nil)
				rm.ReasoningContent = frag
				sw.Send(rm, nil)
			}
		}
		if len(full.ToolCalls) > 0 {
			full.ReasoningContent = "" // already surfaced above; avoid double-emit
			sw.Send(full, nil)
			return
		}
		for _, frag := range chunkRunes(full.Content, 24) {
			sw.Send(schema.AssistantMessage(frag, nil), nil)
		}
	}()
	return sr, nil
}

// chunkRunes splits s into rune-safe fragments of at most size runes each, so a
// one-shot response can be re-emitted as incremental frames without splitting a
// multi-byte character. Returns nil for empty input.
func chunkRunes(s string, size int) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	out := make([]string, 0, (len(runes)+size-1)/size)
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

func (m *chatModel) baseURL(def string) string {
	if m.cfg.BaseURL != "" {
		return strings.TrimRight(m.cfg.BaseURL, "/")
	}
	return def
}

// ---- OpenAI-compatible (DeepSeek) ----

// normalizeMessages collapses adjacent plain-text messages that share a role so
// the request satisfies providers that require strict user/assistant pairing
// (e.g. taiji rejects "m[i]: assistant, m[i+1]: assistant Not paired qa"). Such
// runs arise from restored history: a stretch of turns that produced no model
// reply persists consecutive user messages, and some turns leave consecutive
// assistant messages. Only User/Assistant messages with NO tool_calls and NO
// tool_call_id are merged (joined with a blank line); System and tool-call
// messages pass through untouched so a live turn's assistant→tool_result
// pairing is never disturbed.
func normalizeMessages(input []*schema.Message) []*schema.Message {
	out := make([]*schema.Message, 0, len(input))
	mergeable := func(m *schema.Message) bool {
		if m.Role != schema.User && m.Role != schema.Assistant {
			return false
		}
		return len(m.ToolCalls) == 0 && m.ToolCallID == ""
	}
	for _, in := range input {
		if n := len(out); n > 0 && mergeable(in) && mergeable(out[n-1]) && out[n-1].Role == in.Role {
			prev := out[n-1]
			merged := *prev // copy so we don't mutate the caller's window message
			if merged.Content != "" && in.Content != "" {
				merged.Content += "\n\n" + in.Content
			} else {
				merged.Content += in.Content
			}
			out[n-1] = &merged
			continue
		}
		out = append(out, in)
	}
	return out
}

// openAIBody assembles the chat-completions request body shared by the
// streaming and non-streaming paths.
func (m *chatModel) openAIBody(input []*schema.Message) map[string]any {
	input = normalizeMessages(input)
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
		// Serialize assistant tool calls so multi-turn history stays a valid
		// OpenAI conversation: a role:"tool" message MUST be preceded by the
		// assistant message carrying the matching tool_calls, or the provider
		// rejects the request (400 "messages with role 'tool' must be a response
		// to a preceding message with 'tool_calls'"). Dropping them also erased
		// every past tool use from the model's view of the history, which trained
		// it to stop calling tools (reverse few-shot).
		if len(in.ToolCalls) > 0 {
			tcs := make([]map[string]any, 0, len(in.ToolCalls))
			for _, tc := range in.ToolCalls {
				tcs = append(tcs, map[string]any{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]any{
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					},
				})
			}
			om.ToolCalls = tcs
		}
		msgs = append(msgs, om)
	}
	body := map[string]any{
		"model":    m.cfg.Model,
		"messages": msgs,
	}
	// taiji's DeepSeek-*-Online models only honor OpenAI function-calling when this
	// private field is set; without it they answer via built-in web search and
	// never emit tool_calls (which silently disables this tool-driven agent). It is
	// opt-in per model (config) because standard OpenAI gateways don't recognize it
	// and some (e.g. yunwu) hang when they receive it.
	if m.cfg.OpenAIInfer {
		body["openai_infer"] = true
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
	return body
}

// openAIURL returns the chat-completions endpoint.
func (m *chatModel) openAIURL() string {
	return m.baseURL("https://api.deepseek.com/v1") + "/chat/completions"
}

func (m *chatModel) generateOpenAI(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
	body := m.openAIBody(input)
	raw, err := m.postJSON(ctx, m.openAIURL(), body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				// Reasoning is an alias some OpenAI-compatible vendors (e.g. certain
				// doubao/volcengine gateways) use instead of reasoning_content. We
				// accept either; whichever is present surfaces as the thinking block.
				Reasoning string `json:"reasoning"`
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
	out.ReasoningContent = firstNonEmptyStr(c.ReasoningContent, c.Reasoning)
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

// anthropicBody assembles the Messages API request body shared by the
// streaming and non-streaming paths.
func (m *chatModel) anthropicBody(input []*schema.Message) map[string]any {
	input = normalizeMessages(input)
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
	// Extended thinking is opt-in (config flag): some proxies/model ids reject the
	// thinking field or alter tool-calling when it is set, so default off. When on,
	// max_tokens must exceed budget_tokens (Anthropic constraint), so raise it.
	if m.cfg.Thinking {
		body["max_tokens"] = 3072
		body["thinking"] = map[string]any{
			"type":          "enabled",
			"budget_tokens": 1024,
		}
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
	return body
}

// anthropicURL returns the Messages endpoint. Strips a trailing /v1 from the
// base URL so a proxy configured as https://host/v1 doesn't produce /v1/v1/messages.
func (m *chatModel) anthropicURL() string {
	base := strings.TrimSuffix(m.baseURL("https://api.anthropic.com"), "/v1")
	return base + "/v1/messages"
}

// anthropicHeaders returns the auth/version headers for the Messages API.
func (m *chatModel) anthropicHeaders() map[string]string {
	return map[string]string{
		"x-api-key":         m.cfg.APIKey,
		"anthropic-version": "2023-06-01",
	}
}

func (m *chatModel) generateAnthropic(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
	body := m.anthropicBody(input)
	raw, err := m.postJSONWithHeaders(ctx, m.anthropicURL(), body, m.anthropicHeaders())
	if err != nil {
		return nil, err
	}
	var resp struct {
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			ID       string          `json:"id"`
			Name     string          `json:"name"`
			Input    json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}
	out := schema.AssistantMessage("", nil)
	var text strings.Builder
	var thinking strings.Builder
	for _, c := range resp.Content {
		switch c.Type {
		case "text":
			text.WriteString(c.Text)
		case "thinking":
			thinking.WriteString(c.Thinking)
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
	out.ReasoningContent = thinking.String()
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
	hdr := map[string]string{"Content-Type": "application/json"}
	for k, v := range headers {
		hdr[k] = v
	}
	resp, err := m.sendWithRetry(ctx, url, buf, hdr, maxRetries)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("chat provider %s returned %d: %s", m.cfg.Provider, resp.StatusCode, truncate(string(data), 300))
	}
	return data, nil
}

// maxRetries bounds transient-failure retries (429 / 5xx) for the non-streaming
// one-shot path, which is the last line of defense and must try hard to succeed.
const maxRetries = 4

// streamOpenRetries bounds retries when OPENING a stream. It is deliberately
// small: a stream open that returns 429/5xx degrades to the one-shot Generate
// fallback (which keeps the full maxRetries budget), so burning the full budget
// on the stream endpoint just makes the user wait ~15s before that fallback even
// starts. Some proxies are flaky on their streaming endpoint specifically while
// the non-streaming endpoint stays healthy, so failing over fast is the win.
const streamOpenRetries = 1

// sendWithRetry POSTs body to url, retrying on rate-limit (429) and transient
// server errors (5xx) with exponential backoff, up to maxAttempts retries (so
// maxAttempts+1 total tries). It honors a Retry-After header when present and
// aborts promptly on context cancellation. A fresh request is built each attempt
// because the body reader is consumed on send. The returned response (which the
// caller must close) is the first 2xx, or the last attempt when retries are
// exhausted or the status is non-retryable.
func (m *chatModel) sendWithRetry(ctx context.Context, url string, body []byte, headers map[string]string, maxAttempts int) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= maxAttempts; attempt++ {
		if attempt > 0 {
			delay := m.backoffDelay(attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := m.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("chat request: %w", err)
			continue // network error: retry
		}
		if !retryableStatus(resp.StatusCode) {
			return resp, nil // success or non-retryable error: hand back to caller
		}
		// Retryable: drain+close this body and try again (after honoring Retry-After).
		honorRetryAfter(ctx, resp)
		lastErr = fmt.Errorf("chat provider %s returned %d (attempt %d/%d)", m.cfg.Provider, resp.StatusCode, attempt+1, maxAttempts+1)
		resp.Body.Close()
	}
	return nil, lastErr
}

// retryableStatus reports whether an HTTP status warrants a retry: rate limiting
// (429) and transient upstream errors (502/503/504 and other 5xx).
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// backoffDelay returns an exponential backoff for the given 1-based attempt,
// capped so a stuck provider doesn't stall the request indefinitely.
func (m *chatModel) backoffDelay(attempt int) time.Duration {
	base := m.retryBase
	if base <= 0 {
		base = time.Second
	}
	d := time.Duration(1<<uint(attempt-1)) * base // base, 2x, 4x, 8x
	if cap := 8 * base; d > cap {
		d = cap
	}
	return d
}

// honorRetryAfter sleeps for a server-specified Retry-After (seconds) if present
// and small, so we don't hammer a provider that told us exactly when to return.
func honorRetryAfter(ctx context.Context, resp *http.Response) {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return
	}
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs <= 0 || secs > 30 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Duration(secs) * time.Second):
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// firstNonEmptyStr returns the first non-empty string among the args.
func firstNonEmptyStr(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
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
