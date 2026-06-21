package agent

import (
	"context"

	"github.com/cloudwego/eino/schema"

	"gameasset/internal/config"
)

// chatCompleter adapts a chatModel to copywriting.Completer: a one-shot text
// completion from a fixed system prompt + user message, returning the raw reply
// text. It reuses the same HTTP-backed chat transport the agent uses, so copy
// generation runs on the configured conversation model with no extra wiring.
type chatCompleter struct {
	model *chatModel
}

// NewChatCompleter builds a copywriting.Completer over the given chat model
// config (typically cfg.ChatPrimary). Exported so cmd/server can wire the
// copywriting service without importing the unexported chatModel.
func NewChatCompleter(cfg config.ModelConfig) *chatCompleter {
	return &chatCompleter{model: newChatModel(cfg)}
}

// Complete runs a single non-streaming completion and returns the assistant
// text. Errors propagate so the copywriting service can surface an honest
// failure rather than empty copy.
func (c *chatCompleter) Complete(ctx context.Context, system, user string) (string, error) {
	msgs := []*schema.Message{
		schema.SystemMessage(system),
		schema.UserMessage(user),
	}
	out, err := c.model.Generate(ctx, msgs)
	if err != nil {
		return "", err
	}
	return out.Content, nil
}
