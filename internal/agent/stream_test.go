package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// collectSink captures stream frames for assertions.
type collectSink struct {
	content   strings.Builder
	reasoning strings.Builder
	toolCalls []schema.ToolCall
	err       error
}

func (c *collectSink) Send(m *schema.Message, err error) bool {
	if err != nil {
		c.err = err
		return true
	}
	if m == nil {
		return true
	}
	c.content.WriteString(m.Content)
	c.reasoning.WriteString(m.ReasoningContent)
	c.toolCalls = append(c.toolCalls, m.ToolCalls...)
	return true
}

func TestStreamOpenAITextAndReasoning(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"think "}}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":"more"}}]}`,
		`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
		`data: {"choices":[{"delta":{"content":", world"}}]}`,
		`data: [DONE]`,
	}, "\n")
	var sink collectSink
	if err := streamOpenAI(strings.NewReader(sse), &sink); err != nil {
		t.Fatalf("streamOpenAI: %v", err)
	}
	if got := sink.content.String(); got != "Hello, world" {
		t.Errorf("content = %q, want %q", got, "Hello, world")
	}
	if got := sink.reasoning.String(); got != "think more" {
		t.Errorf("reasoning = %q, want %q", got, "think more")
	}
	if len(sink.toolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(sink.toolCalls))
	}
}

func TestStreamOpenAIToolCallAccumulation(t *testing.T) {
	// Tool name arrives first, arguments stream in fragments under one index.
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"crop_to_sizes"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"source"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"_asset_id\":\"a1\"}"}}]}}]}`,
		`data: [DONE]`,
	}, "\n")
	var sink collectSink
	if err := streamOpenAI(strings.NewReader(sse), &sink); err != nil {
		t.Fatalf("streamOpenAI: %v", err)
	}
	if len(sink.toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(sink.toolCalls))
	}
	tc := sink.toolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "crop_to_sizes" {
		t.Errorf("tool call id/name = %q/%q", tc.ID, tc.Function.Name)
	}
	if tc.Function.Arguments != `{"source_asset_id":"a1"}` {
		t.Errorf("accumulated args = %q", tc.Function.Arguments)
	}
}

func TestStreamAnthropicTextThinkingAndTool(t *testing.T) {
	sse := strings.Join([]string{
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Sure"}}`,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tu_1","name":"edit_image"}}`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"intent\":"}}`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"change_background\"}"}}`,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
	}, "\n")
	var sink collectSink
	if err := streamAnthropic(strings.NewReader(sse), &sink); err != nil {
		t.Fatalf("streamAnthropic: %v", err)
	}
	if got := sink.content.String(); got != "Sure" {
		t.Errorf("content = %q, want %q", got, "Sure")
	}
	if got := sink.reasoning.String(); got != "hmm" {
		t.Errorf("reasoning = %q, want %q", got, "hmm")
	}
	if len(sink.toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(sink.toolCalls))
	}
	if sink.toolCalls[0].Function.Arguments != `{"intent":"change_background"}` {
		t.Errorf("tool args = %q", sink.toolCalls[0].Function.Arguments)
	}
}

func TestStreamOpenAISkipsMalformedLines(t *testing.T) {
	sse := strings.Join([]string{
		`: keep-alive comment`,
		`data: {bad json}`,
		`data: {"choices":[{"delta":{"content":"ok"}}]}`,
		`data: [DONE]`,
	}, "\n")
	var sink collectSink
	if err := streamOpenAI(strings.NewReader(sse), &sink); err != nil {
		t.Fatalf("streamOpenAI: %v", err)
	}
	if got := sink.content.String(); got != "ok" {
		t.Errorf("content = %q, want %q", got, "ok")
	}
}
