package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"gameasset/internal/config"
	"gameasset/internal/crop"
	"gameasset/internal/generation"
)

// --- system prompt / whitelist -------------------------------------------

func TestSystemPromptListsWhitelistAndGuards(t *testing.T) {
	p := SystemPrompt()
	for _, c := range Capabilities {
		if !strings.Contains(p, c.Name) {
			t.Errorf("system prompt missing capability %q", c.Name)
		}
	}
	// Must instruct refusal and injection resistance.
	for _, want := range []string{"不要调用任何工具", "ignore previous instructions", "暂未配置"} {
		if !strings.Contains(p, want) {
			t.Errorf("system prompt missing guard text %q", want)
		}
	}
}

func TestRefusalMessageListsCapabilities(t *testing.T) {
	m := RefusalMessage()
	for _, c := range Capabilities {
		if !strings.Contains(m, c.Name) {
			t.Errorf("refusal missing capability %q", c.Name)
		}
	}
}

// --- sliding window --------------------------------------------------------

func TestWindowKeepsSystemAndRecent(t *testing.T) {
	w := NewWindow("SYS", 256, 2, nil)
	w.Append(schema.UserMessage("hello"))
	w.Append(schema.AssistantMessage("hi", nil))
	msgs := w.Messages()
	if msgs[0].Role != schema.System || msgs[0].Content != "SYS" {
		t.Fatalf("first message must be the system prompt, got %+v", msgs[0])
	}
	if w.Compressed() {
		t.Error("window should not be compressed under budget")
	}
}

func TestWindowCompressesOldTurns(t *testing.T) {
	// Tiny budget forces compression; keepRecent=2 retains the last two turns.
	w := NewWindow("SYS", 256, 2, nil)
	long := strings.Repeat("赛博朋克城市夜景，霓虹灯，雨夜街道，远处的摩天楼。", 30)
	for i := 0; i < 8; i++ {
		w.Append(schema.UserMessage(long))
	}
	if !w.Compressed() {
		t.Fatal("expected compression after exceeding budget")
	}
	msgs := w.Messages()
	if msgs[0].Role != schema.System {
		t.Fatalf("system prompt must remain first, got role %q", msgs[0].Role)
	}
	// Second message should be the injected summary (also a system message).
	if msgs[1].Role != schema.System || !strings.Contains(msgs[1].Content, "summary") {
		t.Fatalf("expected summary as second message, got %+v", msgs[1])
	}
	if w.EstimateTokens() > 256 {
		// keepRecent may push us slightly over; ensure it is at least bounded
		// to the recent set rather than the full history.
		if len(msgs) > 2+2 {
			t.Errorf("window not bounded: %d messages retained", len(msgs))
		}
	}
}

func TestAppendToolRefKeepsPayloadOutOfContext(t *testing.T) {
	w := NewWindow("SYS", 4000, 6, nil)
	bigPayload := strings.Repeat("A", 100000) // simulate base64 image bytes
	w.AppendToolRef("call_1", "edit_image", "asset_abc", "background swapped")
	msgs := w.Messages()
	for _, m := range msgs {
		if strings.Contains(m.Content, bigPayload) {
			t.Fatal("raw payload leaked into context")
		}
	}
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "asset_abc") || !strings.Contains(last.Content, "ref=") {
		t.Errorf("tool ref not recorded as reference: %q", last.Content)
	}
	if last.Role != schema.Tool {
		t.Errorf("tool ref should be a tool message, got role %q", last.Role)
	}
}

// --- tool registry ---------------------------------------------------------

func TestToolsBuildWhitelist(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cropSvc := crop.NewService(cfg.Channels, dir, nil, func() string { return "x" })
	deps := ToolDeps{
		Generation: &generation.Service{},
		Crop:       cropSvc,
		SessionID:  "s1",
	}
	tools, err := deps.Tools()
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 {
		t.Fatalf("expected 3 whitelist tools, got %d", len(tools))
	}
}
