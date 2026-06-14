// Package agent wires conversation orchestration on top of CloudWeGo Eino's
// ReAct agent. It owns the chat-model abstraction, the tool registry, the
// per-session context sliding window, and intent gating (whitelist dispatch +
// polite refusal). The framework is kept behind this thin facade so the rest
// of the app never imports Eino types directly (design D1).
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

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
	"gameasset/internal/store"
	"gameasset/internal/transport"
	"gameasset/internal/usermodel"
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
	cfg        *config.Config
	models     *usermodel.Manager
	gen        *generation.Service
	textToImg  *generation.Service
	crop       *crop.Service
	video      *video.Service
	crawl      *crawl.Service
	store      *store.Store
	newID      func(string) string
	budget     int
	keepRecent int
	hub        *transport.Hub

	mu      sync.Mutex
	windows map[string]*Window
	// cancels holds the cancel func for each session's in-flight turn so an
	// inbound interrupt can abort the model stream / ReAct loop mid-turn.
	cancels map[string]context.CancelFunc
	// turnMu serializes Handle per session so two turns never interleave writes
	// into the same window (one lock per session id).
	turnMu map[string]*sync.Mutex
}

// NewOrchestrator builds the orchestrator from config and backing services. The
// default conversation model comes from config; each session may override it
// (and the per-scene generation models) via the usermodel manager.
func NewOrchestrator(cfg *config.Config, gen *generation.Service, cr *crop.Service, vid *video.Service, cw *crawl.Service, hub *transport.Hub, st *store.Store, newID func(string) string) *Orchestrator {
	mc := cfg.ChatPrimary
	if cfg.UseTestModel {
		mc = cfg.ChatTest
	}
	return &Orchestrator{
		model:      newChatModel(mc),
		cfg:        cfg,
		models:     usermodel.NewManager(cfg, st),
		gen:        gen,
		crop:       cr,
		video:      vid,
		crawl:      cw,
		budget:     cfg.ContextTokenBudget,
		keepRecent: 6,
		hub:        hub,
		store:      st,
		newID:      newID,
		windows:    make(map[string]*Window),
		cancels:    make(map[string]context.CancelFunc),
		turnMu:     make(map[string]*sync.Mutex),
	}
}

// SetTextToImage installs the text-to-image generation service (wan/qwen). When
// left unset, the generate_image_from_text tool stays out of the whitelist and
// the agent politely declines pure text-to-image requests.
func (o *Orchestrator) SetTextToImage(svc *generation.Service) { o.textToImg = svc }

// AvailableModels returns the server-authoritative, credential-filtered model
// catalog grouped by scene, the session's current selection per scene, and the
// server-preselected default model id per scene (used when the session has made
// no selection).
func (o *Orchestrator) AvailableModels(sessionID string) (map[config.ModelScene][]config.CatalogEntry, map[config.ModelScene]string, map[config.ModelScene]string, error) {
	overrides, err := o.models.Overrides(sessionID)
	if err != nil {
		return nil, nil, nil, err
	}
	return o.cfg.AvailableModelsByScene(), overrides, o.cfg.SceneDefaults(), nil
}

// SwitchModel records a session's model selection for a scene (validated against
// the available catalog). When the chat model is switched it kicks off a brief
// self-introduction by the new model so the user immediately perceives the
// change; switching a generation model is silent. The intro runs on its own
// goroutine, serialized behind the session turn lock, so it never interleaves
// with an in-flight turn and can be interrupted by the user's next message.
func (o *Orchestrator) SwitchModel(sessionID string, scene config.ModelScene, modelID string) error {
	if err := o.models.Set(sessionID, scene, modelID); err != nil {
		return err
	}
	if scene == config.SceneChat {
		go o.selfIntroduce(sessionID)
	}
	return nil
}

// selfIntroduce streams a short self-introduction from the session's currently
// selected chat model over the live channel as a normal turn (turn_start →
// message increments → turn_end). It reuses the per-session turn lock so it does
// not overlap a real turn.
func (o *Orchestrator) selfIntroduce(sessionID string) {
	lock := o.sessionTurnLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	o.mu.Lock()
	o.cancels[sessionID] = cancel
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		if o.cancels[sessionID] != nil {
			o.cancels[sessionID] = nil
		}
		o.mu.Unlock()
		cancel()
	}()

	mc, _ := o.models.ChatModel(sessionID)
	model := newChatModel(mc)
	intro := []*schema.Message{
		schema.SystemMessage("你是这个游戏宣发素材生成助手刚切换到的模型。用一到两句简体中文向用户做个简短自我介绍:说明你是哪个模型,并一句话点出你能帮忙做的事(生图/裁剪/生视频/文生图等宣发素材操作)。语气轻松专业,不要罗列、不要用 markdown。"),
		schema.UserMessage("请做个简短的自我介绍。"),
	}

	o.emit(sessionID, transport.Event{Type: transport.EventTurnStart, SessionID: sessionID})

	stream, err := model.Stream(ctx, intro)
	if err != nil {
		o.emitTurnEnd(sessionID, turnEndInfo{replyEmpty: true})
		return
	}
	defer stream.Close()

	var sb strings.Builder
	for {
		chunk, err := stream.Recv()
		if err != nil {
			break
		}
		if chunk == nil || chunk.Content == "" {
			continue
		}
		sb.WriteString(chunk.Content)
		o.emit(sessionID, transport.Event{
			Type:      transport.EventMessage,
			SessionID: sessionID,
			Data:      map[string]any{"text": chunk.Content, "done": false},
		})
	}

	reply := sb.String()
	if ctx.Err() != nil {
		o.emitTurnEnd(sessionID, turnEndInfo{replyEmpty: strings.TrimSpace(reply) == "", cancelled: true})
		return
	}
	replyEmpty := strings.TrimSpace(reply) == ""
	if !replyEmpty {
		// Record the intro as a normal assistant turn so the conversation context
		// stays coherent for subsequent turns.
		o.window(sessionID).Append(schema.AssistantMessage(reply, nil))
		o.persist(sessionID, "assistant", reply)
		o.emit(sessionID, transport.Event{
			Type:      transport.EventMessage,
			SessionID: sessionID,
			Data:      map[string]any{"text": reply, "done": true},
		})
	}
	o.emitTurnEnd(sessionID, turnEndInfo{replyEmpty: replyEmpty})
}

// sessionTurnLock returns the per-session turn mutex (creating on first use), so
// concurrent Handle calls for the same session serialize rather than interleave.
func (o *Orchestrator) sessionTurnLock(sessionID string) *sync.Mutex {
	o.mu.Lock()
	defer o.mu.Unlock()
	m, ok := o.turnMu[sessionID]
	if !ok {
		m = &sync.Mutex{}
		o.turnMu[sessionID] = m
	}
	return m
}

// CancelTurn aborts the in-flight conversation turn for a session, if any. It
// only cancels the turn's model inference / tool loop (via its context); it does
// NOT cancel already-submitted async generation/video tasks (those have their
// own cancel entry points). Safe to call when no turn is running.
func (o *Orchestrator) CancelTurn(sessionID string) {
	o.mu.Lock()
	cancel := o.cancels[sessionID]
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// window returns (creating if needed) the conversation window for a session,
// seeded with the system prompt that encodes the intent whitelist. On first
// build it restores prior conversation history from the store so a reconnecting
// client (or a server restart) resumes the same context; the restored window is
// immediately subject to budget compression like any other.
func (o *Orchestrator) window(sessionID string) *Window {
	o.mu.Lock()
	defer o.mu.Unlock()
	w, ok := o.windows[sessionID]
	if !ok {
		w = NewWindow(SystemPrompt(), o.budget, o.keepRecent, nil)
		o.restoreLocked(sessionID, w)
		o.windows[sessionID] = w
	}
	return w
}

// restoreLocked replays persisted messages into a fresh window. Best-effort: a
// store error just yields an empty (system-only) window rather than failing the
// turn. Must be called with o.mu held.
func (o *Orchestrator) restoreLocked(sessionID string, w *Window) {
	if o.store == nil {
		return
	}
	msgs, err := o.store.ListMessages(sessionID)
	if err != nil {
		log.Printf("agent: restore history session=%s failed: %v", sessionID, err)
		return
	}
	for _, m := range msgs {
		switch m.Role {
		case "user":
			w.Append(schema.UserMessage(m.Content))
		case "assistant":
			w.Append(schema.AssistantMessage(m.Content, nil))
		}
	}
	if len(msgs) > 0 {
		log.Printf("agent: restored %d messages for session=%s", len(msgs), sessionID)
	}
}

// ResetContext discards a session's accumulated conversation history, restoring
// a fresh window seeded only with the system prompt. Workspace assets are
// untouched (this only clears the LLM context window). Persisted history is also
// cleared so the next reconnect does not resurrect the old context.
func (o *Orchestrator) ResetContext(sessionID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.windows[sessionID] = NewWindow(SystemPrompt(), o.budget, o.keepRecent, nil)
	if o.store != nil {
		if err := o.store.DeleteMessages(sessionID); err != nil {
			log.Printf("agent: clear history session=%s failed: %v", sessionID, err)
		}
	}
}

// Handle processes one user message for a session: it appends the message to
// the session window, runs the ReAct agent with session-scoped tools, streams
// the assistant's incremental text and tool-call steps to the session's WS
// connections, and records the final reply in the window.
//
// The agent is rebuilt per call because each tool invocation is bound to this
// session (tools read the session id from context to keep assets isolated).
func (o *Orchestrator) Handle(ctx context.Context, sessionID, userText string, lossless bool) (string, error) {
	// Serialize turns per session so two never interleave writes into the same
	// window. A pending turn waits here until the prior one finishes or is
	// cancelled.
	lock := o.sessionTurnLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	// Make this turn cancellable and register its cancel func so an inbound
	// interrupt (CancelTurn) can abort the model stream / ReAct loop mid-flight.
	ctx, cancel := context.WithCancel(ctx)
	o.mu.Lock()
	o.cancels[sessionID] = cancel
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		if o.cancels[sessionID] != nil {
			// Only clear if it is still ours (a newer turn may have replaced it).
			o.cancels[sessionID] = nil
		}
		o.mu.Unlock()
		cancel()
	}()

	w := o.window(sessionID)
	w.Append(schema.UserMessage(userText))

	ctx = withSession(ctx, sessionID)

	// Emit turn_start immediately (before the model is called) so the frontend
	// can enter a loading state without waiting for the first model increment,
	// which can lag by seconds on generation-intent turns.
	o.emit(sessionID, transport.Event{Type: transport.EventTurnStart, SessionID: sessionID})

	// capsuleAsked is flipped when the model calls clarify_intent, so turn_end
	// can tell the frontend a structured question is awaiting the user's reply.
	capsuleAsked := false
	clarify := func(question string, options []ClarifyOption) {
		capsuleAsked = true
		o.emit(sessionID, transport.Event{
			Type:      transport.EventCapsule,
			SessionID: sessionID,
			Data:      map[string]any{"question": question, "options": options},
		})
	}

	deps := ToolDeps{Generation: o.gen, TextToImage: o.textToImg, Crop: o.crop, Video: o.video, Crawl: o.crawl, SessionID: sessionID, Lossless: lossless, Clarify: clarify}
	// Per-session generation model overrides (image / text-to-image / video).
	// Zero value => the tool uses the service default provider.
	if pc, ok := o.models.ImageModel(sessionID, config.SceneImage); ok {
		deps.ImageOverride = &pc
	}
	if pc, ok := o.models.ImageModel(sessionID, config.SceneTextToImage); ok {
		deps.TextToImageOverride = &pc
	}
	if pc, ok := o.models.ImageModel(sessionID, config.SceneVideo); ok {
		deps.VideoOverride = &pc
	}
	tools, err := deps.Tools()
	if err != nil {
		o.emitTurnEnd(sessionID, turnEndInfo{replyEmpty: true})
		return "", fmt.Errorf("build tools: %w", err)
	}

	// Resolve the chat model for THIS turn from the session's selection (falling
	// back to the server default). Constructing a chatModel is cheap (config +
	// http client), and building it per turn means an in-flight turn keeps its own
	// model instance while the next turn picks up a freshly switched one.
	turnModel := o.model
	if mc, overridden := o.models.ChatModel(sessionID); overridden {
		turnModel = newChatModel(mc)
	}

	ra, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: turnModel,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: tools},
		MaxStep:          12,
		// The default checker only inspects the FIRST non-empty stream chunk for
		// tool calls. Our model (deepseek via proxy, and Claude) often emits some
		// reply/thinking text BEFORE the tool_call chunk, so the default wrongly
		// concludes "no tool call" and routes straight to END — the tool node
		// never runs even though the model did request a tool. Scan the whole
		// stream instead (eino documents this exact pitfall on NewAgent).
		StreamToolCallChecker: fullStreamToolCallChecker,
		// edit_image / image_to_video / crawl_game_assets only START an async task
		// (progress tracked over SSE). Return their result directly to the user
		// instead of feeding {status:queued} back to the model — otherwise a small
		// model reads "queued" as "not done yet" and re-invokes the tool in a loop,
		// spawning endless generations.
		ToolReturnDirectly: AsyncTaskTools(),
	})
	if err != nil {
		o.emitTurnEnd(sessionID, turnEndInfo{replyEmpty: true})
		return "", fmt.Errorf("build react agent: %w", err)
	}

	// Tool-execution callback: surface each tool's completion (success/error)
	// so the frontend can stop the spinner and show the action trajectory.
	toolCB := o.toolCallbackHandler(sessionID)

	stream, err := ra.Stream(ctx, w.Messages(), agentOption(toolCB))
	if err != nil {
		o.emitTurnEnd(sessionID, turnEndInfo{replyEmpty: true})
		return "", fmt.Errorf("agent stream: %w", err)
	}
	defer stream.Close()

	var sb strings.Builder
	toolCalls := 0
	chunks := 0
	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err != io.EOF {
				// Surface the real upstream/transport error instead of silently
				// ending the turn with an empty reply (root-cause diagnostics).
				log.Printf("agent: session=%s stream recv error after %d chunks: %v", sessionID, chunks, err)
			}
			break // io.EOF or stream end
		}
		if chunk == nil {
			continue
		}
		chunks++
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
			toolCalls++
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
	cancelled := ctx.Err() != nil
	// Diagnostic: surface whether this turn actually invoked any tool. A request
	// that should produce an asset but emitted 0 tool calls means the model only
	// replied with text (e.g. a small model that "confirms" without acting) —
	// this is the signal to inspect the model/prompt, not the workspace pipeline.
	log.Printf("agent: session=%s turn done toolCalls=%d replyLen=%d chunks=%d capsule=%t cancelled=%t", sessionID, toolCalls, len(reply), chunks, capsuleAsked, cancelled)

	if cancelled {
		// Interrupted mid-turn: persist whatever was produced (keeps context
		// coherent for the next turn) but tell the frontend the turn was
		// cancelled so it can close the loading state without a stray bubble.
		w.Append(schema.AssistantMessage(reply, nil))
		o.persist(sessionID, "user", userText)
		if strings.TrimSpace(reply) != "" {
			o.persist(sessionID, "assistant", reply)
		}
		o.emitTurnEnd(sessionID, turnEndInfo{toolCalls: toolCalls, hasCapsule: capsuleAsked, replyEmpty: strings.TrimSpace(reply) == "", cancelled: true})
		return reply, nil
	}

	replyEmpty := strings.TrimSpace(reply) == ""
	w.Append(schema.AssistantMessage(reply, nil))
	// Only emit a final body message when there is actual text; an empty
	// done-message would otherwise render as a blank assistant bubble (the
	// frontend also guards via turn_end.replyEmpty, but suppressing here keeps
	// the wire clean).
	if !replyEmpty {
		o.emit(sessionID, transport.Event{
			Type:      transport.EventMessage,
			SessionID: sessionID,
			Data:      map[string]any{"text": reply, "done": true},
		})
	}
	// Persist this turn so the conversation survives a server restart / reconnect.
	// Only text is stored (large tool payloads stay out of the DB, mirroring D3).
	o.persist(sessionID, "user", userText)
	if !replyEmpty {
		o.persist(sessionID, "assistant", reply)
	}
	o.emitTurnEnd(sessionID, turnEndInfo{toolCalls: toolCalls, hasCapsule: capsuleAsked, replyEmpty: replyEmpty})
	return reply, nil
}

// persist writes one conversation message to the store (best-effort: a failure
// is logged but does not fail the turn). Empty content is skipped.
func (o *Orchestrator) persist(sessionID, role, content string) {
	if o.store == nil || strings.TrimSpace(content) == "" {
		return
	}
	id := "msg"
	if o.newID != nil {
		id = o.newID("msg")
	}
	if err := o.store.InsertMessage(store.MessageRecord{
		ID:        id,
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		log.Printf("agent: persist message session=%s role=%s failed: %v", sessionID, role, err)
	}
}

// turnEndInfo carries the metadata sent with a turn_end event.
type turnEndInfo struct {
	toolCalls  int
	hasCapsule bool
	replyEmpty bool // no body text produced (frontend suppresses the empty bubble)
	cancelled  bool // the turn was interrupted by the user
}

// emitTurnEnd sends a turn_end event carrying turn metadata (tool usage, whether
// a clarify capsule was produced, whether the reply was empty or the turn was
// cancelled) and the latest context window state, so the frontend can close its
// loading state, suppress empty bubbles, and refresh the context indicator.
func (o *Orchestrator) emitTurnEnd(sessionID string, info turnEndInfo) {
	st := o.State(sessionID)
	o.emit(sessionID, transport.Event{
		Type:      transport.EventTurnEnd,
		SessionID: sessionID,
		Data: map[string]any{
			"toolUsed":   info.toolCalls > 0,
			"hasCapsule": info.hasCapsule,
			"replyEmpty": info.replyEmpty,
			"cancelled":  info.cancelled,
			"context":    st,
		},
	})
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

// fullStreamToolCallChecker reports whether a model's streaming output contains
// any tool call, scanning the ENTIRE stream rather than only the first chunk.
//
// The react agent's default checker (firstChunkStreamToolCallChecker) returns
// false as soon as it sees a non-empty content chunk with no tool calls. Models
// that emit reply/thinking text before the tool_call chunk (deepseek via our
// proxy, Claude) therefore get misrouted to END and their tool never executes.
// We accumulate across the whole stream so a tool call appearing in any later
// chunk is still detected. eino's NewAgent doc calls out this exact pitfall.
func fullStreamToolCallChecker(_ context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
	defer sr.Close()
	for {
		msg, err := sr.Recv()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if len(msg.ToolCalls) > 0 {
			return true, nil
		}
	}
}

// toolKind maps a tool name to the long-task kind its successful result
// produces, or "" when the tool returns immediately (no async task). Used to
// tag tool_result events so the frontend can insert the right placeholder.
func toolKind(name string) string {
	switch name {
	case "edit_image":
		return "generate"
	case "image_to_video":
		return "video"
	case "crawl_game_assets":
		return "crawl"
	default:
		return ""
	}
}

// taskIDFromResponse pulls the task_id out of a tool's JSON result. Long-task
// tools (edit_image/image_to_video/crawl_game_assets) all return a {"task_id":...}
// shaped object; immediate tools return arrays/other shapes with no task_id, so
// this yields "" for them.
func taskIDFromResponse(resp string) string {
	var parsed struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		return ""
	}
	return parsed.TaskID
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
			data := map[string]any{
				"name":    name,
				"status":  "done",
				"summary": summary,
			}
			// Surface the produced long-task id + kind so the frontend can
			// insert a placeholder and subscribe to progress immediately,
			// without waiting for the turn to finish (design D1).
			if output != nil {
				if taskID := taskIDFromResponse(output.Response); taskID != "" {
					data["task_id"] = taskID
					if kind := toolKind(name); kind != "" {
						data["kind"] = kind
					}
				}
			}
			o.emit(sessionID, transport.Event{
				Type:      transport.EventToolResult,
				SessionID: sessionID,
				Data:      data,
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
