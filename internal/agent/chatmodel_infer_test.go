package agent

import (
	"testing"

	"github.com/cloudwego/eino/schema"

	"gameasset/internal/config"
)

// TestOpenAIBodyInjectsInferFlag verifies the taiji-private openai_infer field is
// added to the chat-completions body only when the model opts in. taiji's
// DeepSeek-*-Online models silently ignore tools (routing through built-in web
// search) unless this flag is set, while standard gateways don't recognize it,
// so the injection must be gated on ModelConfig.OpenAIInfer.
func TestOpenAIBodyInjectsInferFlag(t *testing.T) {
	input := []*schema.Message{schema.UserMessage("hi")}

	t.Run("on", func(t *testing.T) {
		m := &chatModel{cfg: config.ModelConfig{Provider: "openai", Model: "x", OpenAIInfer: true}}
		body := m.openAIBody(input)
		v, ok := body["openai_infer"]
		if !ok {
			t.Fatal("expected openai_infer key when OpenAIInfer=true")
		}
		if b, _ := v.(bool); !b {
			t.Fatalf("expected openai_infer=true, got %v", v)
		}
	})

	t.Run("off", func(t *testing.T) {
		m := &chatModel{cfg: config.ModelConfig{Provider: "openai", Model: "x", OpenAIInfer: false}}
		body := m.openAIBody(input)
		if _, ok := body["openai_infer"]; ok {
			t.Fatal("openai_infer must be absent when OpenAIInfer=false (standard gateways reject it)")
		}
	})
}
