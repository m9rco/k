package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestStreamOpenAISurfacesEmbeddedError verifies that a gateway error delivered
// inside a 200 SSE body (e.g. taiji's "Not paired qa") is surfaced as an error
// rather than silently skipped — the regression that produced unexplained
// chunks=0 turns.
func TestStreamOpenAISurfacesEmbeddedError(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"error":{"code":"m[13]: assistant, m[14]: assistant Not paired qa","message":"m[13]: assistant, m[14]: assistant Not paired qa","type":"RequestFormatError"}}`,
	}, "\n")
	var sink collectSink
	err := streamOpenAI(strings.NewReader(sse), &sink)
	if err == nil {
		t.Fatal("expected streamOpenAI to return the embedded error, got nil")
	}
	if !strings.Contains(err.Error(), "Not paired qa") {
		t.Errorf("error = %q, want it to mention the gateway message", err)
	}
	if !strings.Contains(err.Error(), "RequestFormatError") {
		t.Errorf("error = %q, want it to mention the error type", err)
	}
}

// TestStreamAnthropicSurfacesEmbeddedError mirrors the OpenAI case for the
// Messages-API SSE parser.
func TestStreamAnthropicSurfacesEmbeddedError(t *testing.T) {
	sse := strings.Join([]string{
		`event: error`,
		`data: {"type":"error","error":{"type":"overloaded_error","message":"upstream overloaded"}}`,
	}, "\n")
	var sink collectSink
	err := streamAnthropic(strings.NewReader(sse), &sink)
	if err == nil {
		t.Fatal("expected streamAnthropic to return the embedded error, got nil")
	}
	if !strings.Contains(err.Error(), "upstream overloaded") {
		t.Errorf("error = %q, want it to mention the gateway message", err)
	}
}

// TestStreamOpenAINormalErrorWordNotMisread guards sseError's cheap guard: a
// normal content frame that merely contains the substring "error" in its text
// must not be misclassified as a provider error.
func TestStreamOpenAIContentMentioningErrorIsNotAnError(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"the error was fixed"}}]}`,
		`data: [DONE]`,
	}, "\n")
	var sink collectSink
	if err := streamOpenAI(strings.NewReader(sse), &sink); err != nil {
		t.Fatalf("streamOpenAI returned error on benign content: %v", err)
	}
	if got := sink.content.String(); got != "the error was fixed" {
		t.Errorf("content = %q, want %q", got, "the error was fixed")
	}
}

func TestNormalizeMessagesMergesConsecutiveSameRole(t *testing.T) {
	in := []*schema.Message{
		schema.SystemMessage("sys"),
		schema.UserMessage("u1"),
		schema.UserMessage("u2"),
		schema.UserMessage("u3"),
		schema.AssistantMessage("a1", nil),
		schema.AssistantMessage("a2", nil),
		schema.UserMessage("u4"),
	}
	out := normalizeMessages(in)
	wantRoles := []schema.RoleType{schema.System, schema.User, schema.Assistant, schema.User}
	if len(out) != len(wantRoles) {
		t.Fatalf("len(out) = %d, want %d (%v)", len(out), len(wantRoles), out)
	}
	for i, r := range wantRoles {
		if out[i].Role != r {
			t.Errorf("out[%d].Role = %q, want %q", i, out[i].Role, r)
		}
	}
	if out[1].Content != "u1\n\nu2\n\nu3" {
		t.Errorf("merged user content = %q, want %q", out[1].Content, "u1\n\nu2\n\nu3")
	}
	if out[2].Content != "a1\n\na2" {
		t.Errorf("merged assistant content = %q, want %q", out[2].Content, "a1\n\na2")
	}
}

// TestNormalizeMessagesPreservesToolPairing ensures an in-turn
// assistant(tool_calls) → tool(result) sequence is never merged, so live tool
// calling keeps working.
func TestNormalizeMessagesPreservesToolPairing(t *testing.T) {
	idx := 0
	asstWithTool := schema.AssistantMessage("", []schema.ToolCall{{
		Index:    &idx,
		ID:       "call_1",
		Function: schema.FunctionCall{Name: "edit_image", Arguments: `{"prompt":"cat"}`},
	}})
	toolResult := schema.ToolMessage("[edit_image result ref=x] ok", "call_1")
	in := []*schema.Message{
		schema.UserMessage("draw a cat"),
		asstWithTool,
		toolResult,
		schema.AssistantMessage("done", nil),
	}
	out := normalizeMessages(in)
	if len(out) != len(in) {
		t.Fatalf("len(out) = %d, want %d — tool-call pairing must not be merged", len(out), len(in))
	}
	if len(out[1].ToolCalls) != 1 || out[1].ToolCalls[0].ID != "call_1" {
		t.Errorf("assistant tool_calls lost after normalize: %+v", out[1])
	}
	if out[2].ToolCallID != "call_1" {
		t.Errorf("tool_call_id lost after normalize: %q", out[2].ToolCallID)
	}
}

// TestNormalizeMessagesDoesNotMutateInput verifies the caller's window messages
// are untouched (we merge into copies).
func TestNormalizeMessagesDoesNotMutateInput(t *testing.T) {
	u1 := schema.UserMessage("u1")
	u2 := schema.UserMessage("u2")
	in := []*schema.Message{u1, u2}
	_ = normalizeMessages(in)
	if u1.Content != "u1" {
		t.Errorf("input message mutated: u1.Content = %q, want %q", u1.Content, "u1")
	}
}
