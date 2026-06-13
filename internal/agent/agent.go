// Package agent wires conversation orchestration on top of CloudWeGo Eino's
// ReAct agent. It owns the chat-model abstraction, the tool registry, the
// per-session context sliding window, and intent gating (whitelist dispatch +
// polite refusal). The framework is kept behind this thin facade so the rest
// of the app never imports Eino types directly (design D1).
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	einoagent "github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	utilcb "github.com/cloudwego/eino/utils/callbacks"

	"gameasset/internal/config"
	"gameasset/internal/crawl"
	"gameasset/internal/crop"
	"gameasset/internal/generation"
	"gameasset/internal/transport"
	"gameasset/internal/video"
)

// sessionKey scopes a tool invocation to its caller's session via context.
type sessionKey struct{}

func withSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionKey{}, sessionID)
}

// Orchestrator drives a single app-wide agent definition (model + tools +
// system prompt). Per-session conversation windows are held in memory keyed by
// session id; large tool results never enter the LLM context as raw bytes,
// only reference ids (design D3).
type Orchestrator struct {
	model      *chatModel
	gen        *generation.Service
	crop       *crop.Service
	video      *video.Service
	crawl      *crawl.Service
	budget     int
	keepRecent int
	hub        *transport.Hub

	mu      sync.Mutex
	windows map[string]*Window
}

// NewOrchestrator builds the orchestrator from config and backing services.
// The conversation model is selected from config (primary unless the test
// model is enabled); users cannot switch it (requirement: model hardcoded).
func NewOrchestrator(cfg *config.Config, gen *generation.Service, cr *crop.Service, vid *video.Service, cw *crawl.Service, hub *transport.Hub) *Orchestrator {
	mc := cfg.ChatPrimary
	if cfg.UseTestModel {
		mc = cfg.ChatTest
	}
	return &Orchestrator{
		model:      newChatModel(mc),
		gen:        gen,
		crop:       cr,
		video:      vid,
		crawl:      cw,
		budget:     cfg.ContextTokenBudget,
		keepRecent: 6,
		hub:        hub,
		windows:    make(map[string]*Window),
	}
}

// window returns (creating if needed) the conversation window for a session,
// seeded with the system prompt that encodes the intent whitelist.
func (o *Orchestrator) window(sessionID string) *Window {
	o.mu.Lock()
	defer o.mu.Unlock()
	w, ok := o.windows[sessionID]
	if !ok {
		w = NewWindow(SystemPrompt(), o.budget, o.keepRecent, nil)
		o.windows[sessionID] = w
	}
	return w
}

// ResetContext discards a session's accumulated conversation history, restoring
// a fresh window seeded only with the system prompt. Workspace assets are
// untouched (this only clears the LLM context window).
func (o *Orchestrator) ResetContext(sessionID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.windows[sessionID] = NewWindow(SystemPrompt(), o.budget, o.keepRecent, nil)
}

// Handle processes one user message for a session: it appends the message to
// the session window, runs the ReAct agent with session-scoped tools, streams
// the assistant's incremental text and tool-call steps to the session's WS
// connections, and records the final reply in the window.
//
// The agent is rebuilt per call because each tool invocation is bound to this
// session (tools read the session id from context to keep assets isolated).
func (o *Orchestrator) Handle(ctx context.Context, sessionID, userText string, lossless bool) (string, error) {
	w := o.window(sessionID)
	w.Append(schema.UserMessage(userText))

	ctx = withSession(ctx, sessionID)

	deps := ToolDeps{Generation: o.gen, Crop: o.crop, Video: o.video, Crawl: o.crawl, SessionID: sessionID, Lossless: lossless}
	tools, err := deps.Tools()
	if err != nil {
		return "", fmt.Errorf("build tools: %w", err)
	}

	ra, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: o.model,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: tools},
		MaxStep:          12,
	})
	if err != nil {
		return "", fmt.Errorf("build react agent: %w", err)
	}

	// Tool-execution callback: surface each tool's completion (success/error)
	// so the frontend can stop the spinner and show the action trajectory.
	toolCB := o.toolCallbackHandler(sessionID)

	stream, err := ra.Stream(ctx, w.Messages(), agentOption(toolCB))
	if err != nil {
		return "", fmt.Errorf("agent stream: %w", err)
	}
	defer stream.Close()

	var sb strings.Builder
	for {
		chunk, err := stream.Recv()
		if err != nil {
			break // io.EOF or stream end
		}
		if chunk == nil {
			continue
		}
		if chunk.ReasoningContent != "" {
			o.emit(sessionID, transport.Event{
				Type:      transport.EventReasoning,
				SessionID: sessionID,
				Data:      map[string]any{"text": chunk.ReasoningContent},
			})
		}
		for _, tc := range chunk.ToolCalls {
			if tc.Function.Name == "" {
				continue
			}
			o.emit(sessionID, transport.Event{
				Type:      transport.EventToolCall,
				SessionID: sessionID,
				Data: map[string]string{
					"id":        tc.ID,
					"name":      tc.Function.Name,
					"arguments": truncate(tc.Function.Arguments, 400),
				},
			})
		}
		if chunk.Content != "" {
			sb.WriteString(chunk.Content)
			o.emit(sessionID, transport.Event{
				Type:      transport.EventMessage,
				SessionID: sessionID,
				Data:      map[string]any{"text": chunk.Content, "done": false},
			})
		}
	}

	reply := sb.String()
	w.Append(schema.AssistantMessage(reply, nil))
	o.emit(sessionID, transport.Event{
		Type:      transport.EventMessage,
		SessionID: sessionID,
		Data:      map[string]any{"text": reply, "done": true},
	})
	return reply, nil
}

// emit sends an event to the session's WS connections when a hub is present.
func (o *Orchestrator) emit(sessionID string, ev transport.Event) {
	if o.hub != nil {
		o.hub.Send(sessionID, ev)
	}
}

// agentOption wraps a callbacks handler as a react agent option.
func agentOption(h callbacks.Handler) einoagent.AgentOption {
	return einoagent.WithComposeOptions(compose.WithCallbacks(h))
}

// toolCallbackHandler builds a handler that emits a tool_result event whenever
// a tool finishes (success or error), so the UI can complete the action card.
func (o *Orchestrator) toolCallbackHandler(sessionID string) callbacks.Handler {
	return utilcb.NewHandlerHelper().Tool(&utilcb.ToolCallbackHandler{
		OnEnd: func(ctx context.Context, info *callbacks.RunInfo, output *tool.CallbackOutput) context.Context {
			name := ""
			if info != nil {
				name = info.Name
			}
			summary := ""
			if output != nil {
				summary = truncate(output.Response, 200)
			}
			o.emit(sessionID, transport.Event{
				Type:      transport.EventToolResult,
				SessionID: sessionID,
				Data: map[string]any{
					"name":    name,
					"status":  "done",
					"summary": summary,
				},
			})
			return ctx
		},
		OnError: func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			name := ""
			if info != nil {
				name = info.Name
			}
			o.emit(sessionID, transport.Event{
				Type:      transport.EventToolResult,
				SessionID: sessionID,
				Data: map[string]any{
					"name":   name,
					"status": "error",
					"error":  truncate(err.Error(), 200),
				},
			})
			return ctx
		},
	}).Handler()
}

// ContextState is a snapshot of a session's context window for the UI panel.
type ContextState struct {
	EstimatedTokens int  `json:"estimatedTokens"`
	Budget          int  `json:"budget"`
	Compressed      bool `json:"compressed"`
}

// State returns the context window snapshot for a session.
func (o *Orchestrator) State(sessionID string) ContextState {
	w := o.window(sessionID)
	return ContextState{
		EstimatedTokens: w.EstimateTokens(),
		Budget:          o.budget,
		Compressed:      w.Compressed(),
	}
}
