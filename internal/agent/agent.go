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
	"gameasset/internal/cos"
	"gameasset/internal/crawl"
	"gameasset/internal/crop"
	"gameasset/internal/generation"
	applog "gameasset/internal/log"
	"gameasset/internal/store"
	"gameasset/internal/transport"
	"gameasset/internal/usermodel"
	"gameasset/internal/video"
	"gameasset/internal/vision"
	"gameasset/internal/websearch"
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
	webSearch  *websearch.Service
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
	// lastProduced tracks the most recently produced asset_id per session so
	// follow-up turns default to editing the latest output rather than forcing
	// the user to re-select it. In-memory only; survives across turns within a
	// process lifetime (loss on restart is acceptable).
	lastProduced map[string]string
	// refPublisher / visionAnalyzer back the platform-adaptation vision pre-stage
	// (publish refs to COS → analyze marketing elements). Both optional; nil
	// disables the pre-stage so adaptation falls back to the standard harness.
	refPublisher   *cos.Uploader
	visionAnalyzer *vision.Analyzer
	// summaryConfirms holds the per-(session|cacheKey) channel a gated
	// adapt_to_platform call waits on after producing a live analysis report. The
	// inbound "summary_confirm" handler delivers the user's final summary (edited
	// or countdown-default) here to release the gate before AI repaint begins.
	summaryConfirms map[string]chan summaryConfirm
}

// summaryConfirm carries the user's decision from the editable analysis panel's
// confirmation window back to the gated adapt_to_platform call.
type summaryConfirm struct {
	summary string
	edited  bool
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
		model:           newChatModel(mc),
		cfg:             cfg,
		models:          usermodel.NewManager(cfg, st),
		gen:             gen,
		crop:            cr,
		video:           vid,
		crawl:           cw,
		budget:          cfg.ContextTokenBudget,
		keepRecent:      6,
		hub:             hub,
		store:           st,
		newID:           newID,
		windows:         make(map[string]*Window),
		cancels:         make(map[string]context.CancelFunc),
		turnMu:          make(map[string]*sync.Mutex),
		lastProduced:    make(map[string]string),
		summaryConfirms: make(map[string]chan summaryConfirm),
	}
}

// SetLastProduced records the most recently produced asset_id for a session.
// Called by generation/video services via callback when a task completes.
func (o *Orchestrator) SetLastProduced(sessionID, assetID string) {
	o.mu.Lock()
	o.lastProduced[sessionID] = assetID
	o.mu.Unlock()
}

// LastProduced returns the most recently produced asset_id for a session, or ""
// if none has been produced in this process lifetime.
func (o *Orchestrator) LastProduced(sessionID string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastProduced[sessionID]
}

// SetWebSearch installs the web-search service (DDG text + Bing images).
func (o *Orchestrator) SetWebSearch(svc *websearch.Service) { o.webSearch = svc }

// SetTextToImage installs the text-to-image generation service (wan/qwen). When
// left unset, the generate_image_from_text tool stays out of the whitelist and
// the agent politely declines pure text-to-image requests.
func (o *Orchestrator) SetTextToImage(svc *generation.Service) { o.textToImg = svc }

// SetRefPublisher installs the COS uploader used by the platform-adaptation
// vision pre-stage to publish source images for analysis. Optional.
func (o *Orchestrator) SetRefPublisher(u *cos.Uploader) { o.refPublisher = u }

// SetVisionAnalyzer installs the grok-4-fast analyzer used by the
// platform-adaptation vision pre-stage. Optional; nil disables analysis.
func (o *Orchestrator) SetVisionAnalyzer(a *vision.Analyzer) { o.visionAnalyzer = a }

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

	o.emit(sessionID, transport.Event{Type: transport.EventTurnStart, SessionID: sessionID, Data: map[string]any{"streaming": true}})

	// Degrade notifier: if the model falls back from streaming to a one-shot
	// response, tell the frontend this turn is non-streaming so it switches to the
	// static fallback deterministically (rather than waiting on its timeout).
	ctx = withDegradeNotifier(ctx, o.degradeNotifier(sessionID))

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
		// The self-introduction is a UI courtesy on model switch — it is NOT part
		// of the task conversation, so it is deliberately NOT appended to the
		// window or persisted. Recording it would seed the history with
		// task-irrelevant chit-chat that dilutes the "call a tool" signal and, after
		// a restart, reverse-trains the model to reply in prose (see restoreLocked).
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

// confirmKey scopes a pending summary confirmation to a session + cache key, so
// concurrent sessions (or, in theory, two image groups) never cross-deliver.
func confirmKey(sessionID, cacheKey string) string { return sessionID + "|" + cacheKey }

// awaitSummaryConfirm registers a confirmation channel for (sessionID, cacheKey)
// and blocks until the frontend delivers the user's decision (confirm, edit, or
// countdown-default), the turn context is cancelled (user interrupt), or the
// server safety timeout elapses. It returns the final summary and whether it was
// edited; on cancel/timeout it returns (original, false) so adaptation proceeds
// with the grok report rather than hanging. The channel is always unregistered
// before returning. The 3s countdown lives entirely on the frontend; this 8s
// timeout is only a backstop for an absent/old client whose confirm never
// arrives — it sits just above the 3s window so a stalled client costs a few
// seconds, not a minute (see realtime-transport).
func (o *Orchestrator) awaitSummaryConfirm(ctx context.Context, sessionID, cacheKey, original string) (string, bool) {
	key := confirmKey(sessionID, cacheKey)
	ch := make(chan summaryConfirm, 1)
	o.mu.Lock()
	o.summaryConfirms[key] = ch
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		delete(o.summaryConfirms, key)
		o.mu.Unlock()
	}()

	// Backstop only for an absent/old client (the real 3s countdown lives on the
	// frontend). Kept just above the frontend window so a normal confirm always
	// wins the race; if the client never echoes back, adaptation proceeds in a few
	// seconds instead of stalling a full minute.
	const safetyTimeout = 8 * time.Second
	timer := time.NewTimer(safetyTimeout)
	defer timer.Stop()
	select {
	case c := <-ch:
		summary := strings.TrimSpace(c.summary)
		if summary == "" {
			return original, false
		}
		return summary, c.edited
	case <-ctx.Done():
		return original, false
	case <-timer.C:
		return original, false
	}
}

// DeliverSummaryConfirm routes an inbound "summary_confirm" message to the gated
// adapt_to_platform call waiting on (sessionID, cacheKey). It is non-blocking and
// a no-op when no call is waiting (stale/duplicate confirm), so a late frontend
// message after the safety timeout is harmlessly dropped.
func (o *Orchestrator) DeliverSummaryConfirm(sessionID, cacheKey, summary string, edited bool) {
	o.mu.Lock()
	ch := o.summaryConfirms[confirmKey(sessionID, cacheKey)]
	o.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- summaryConfirm{summary: summary, edited: edited}:
	default:
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
			// A turn that called tools persisted its calls in ToolRefs; rebuild the
			// assistant{tool_calls}→tool structure so the restored window is a valid
			// tool exchange and the model keeps seeing that past turns used tools
			// (otherwise history collapses to bare text acks and reverse-trains the
			// model to stop calling tools — the root cause this restores from).
			if m.ToolRefs != "" {
				var calls []turnToolCall
				if err := json.Unmarshal([]byte(m.ToolRefs), &calls); err == nil && len(calls) > 0 {
					for _, rm := range turnAssistantMessages(m.Content, calls) {
						w.Append(rm)
					}
					continue
				}
				// Malformed refs: fall through to plain text rather than drop the turn.
			}
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
	// Drop the sticky last-produced anchor too: a fresh window must not keep
	// injecting "[上次产物]" from the discarded context (sticky-last-output).
	delete(o.lastProduced, sessionID)
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
	// Deterministic pre-classification: nudge a weak model toward the right tool
	// by injecting an advisory "意图提示" prefix, and capture the result to drive
	// the post-turn remediation loop (clarify vs refuse). Advisory only — the model
	// remains the decision-maker (see intent.go, SystemPrompt rule 10).
	hint := ClassifyIntent(userText)
	turnText := userText
	hintInjected := false
	if prefix := BuildIntentHint(hint); prefix != "" {
		turnText = prefix + " " + userText
		hintInjected = true
	}
	applog.From(ctx).Info().
		Str("event", "intent.classify").
		Str("intent", intentLabel(hint)).
		Bool("hint_injected", hintInjected).
		Msg("intent pre-classified")
	// Feedback-driven remediation: when the user reports that a previous operation
	// never actually produced anything AND the previous assistant turn in fact
	// called no tool (the fake-exec pattern), inject an advisory hint nudging the
	// model to really call the tool this turn instead of confirming in prose again.
	// Checked against the live window BEFORE appending this turn's user message.
	if looksLikeMissingOutputComplaint(userText) && !prevTurnHadToolCall(w.Messages()) {
		applog.From(ctx).Warn().
			Str("event", "remediation.missing_output_hint").
			Str("intent", intentLabel(hint)).
			Msg("missing-output complaint after a zero-tool turn, injecting remediation hint")
		turnText = BuildRemediationHint(hint) + " " + turnText
	}
	w.Append(schema.UserMessage(turnText))
	// Surface any compression that just happened with the turn's trace context —
	// a truncated window is a prime suspect when the model later hallucinates.
	for _, ce := range w.DrainCompressions() {
		applog.From(ctx).Info().
			Str("event", "window.compress").
			Int("before_msgs", ce.BeforeMsgs).
			Int("after_msgs", ce.AfterMsgs).
			Int("folded", ce.Folded).
			Int("summary_len", ce.SummaryLen).
			Bool("tool_exchange_kept", ce.ToolExchangeKept).
			Msg("context window compressed")
	}

	ctx = withSession(ctx, sessionID)

	// Emit turn_start immediately (before the model is called) so the frontend
	// can enter a loading state without waiting for the first model increment,
	// which can lag by seconds on generation-intent turns. streaming:true marks
	// the default (real-streaming) path; a degrade flips it (see below).
	o.emit(sessionID, transport.Event{Type: transport.EventTurnStart, SessionID: sessionID, Data: map[string]any{"streaming": true}})

	// Degrade notifier: if the model falls back from streaming to a one-shot
	// response mid-turn, signal the frontend so it switches to the static
	// fallback deterministically instead of waiting on its timeout.
	ctx = withDegradeNotifier(ctx, o.degradeNotifier(sessionID))

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

	deps := ToolDeps{Generation: o.gen, TextToImage: o.textToImg, Crop: o.crop, Video: o.video, Crawl: o.crawl, WebSearch: o.webSearch, Store: o.store, SessionID: sessionID, Lossless: lossless, Clarify: clarify, dedup: newTurnCallGuard()}
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
	// Request-scoped routing: adapt_to_platform's AI repaint path defaults to
	// gpt-image-2 (best subject/composition preservation) regardless of the
	// session's image-scene selection. ResolveImageModel returns ok only when a
	// model is available (its scene credential is configured), so a missing key
	// never injects a broken override. Fallback order: gpt-image-2 →
	// gemini-3-pro-image → nil (AdaptModelOverride stays nil, so adaptProvider
	// falls through to ImageOverride: the session image选择 or service default).
	// edit_image and other image tools are unaffected (they use ImageOverride).
	if pc, ok := o.cfg.ResolveImageModel(config.SceneImage, "gpt-image-2"); ok {
		deps.AdaptModelOverride = &pc
	} else if pc, ok := o.cfg.ResolveImageModel(config.SceneImage, "gemini-3-pro-image"); ok {
		deps.AdaptModelOverride = &pc
	}
	// Vision pre-stage for platform adaptation (publish → analyze → theme inject).
	deps.RefPublisher = o.refPublisher
	deps.VisionAnalyzer = o.visionAnalyzer
	if o.hub != nil {
		sid := sessionID
		deps.Notify = func(text string, done bool) {
			o.hub.Send(sid, transport.Event{
				Type:      transport.EventMessage,
				SessionID: sid,
				Data:      map[string]any{"text": text, "done": done},
			})
		}
		deps.NotifyAnalysis = func(text string, done bool) {
			o.hub.Send(sid, transport.Event{
				Type:      transport.EventMessage,
				SessionID: sid,
				Data:      map[string]any{"text": text, "done": done, "analysis": true},
			})
		}
		// Gate adapt_to_platform between a live analysis report and AI repaint:
		// emit a summary_confirm signal (carrying the cache key) so the frontend's
		// editable analysis panel starts its 3s countdown, then block until the
		// user confirms/edits, the countdown auto-submits, or the safety timeout
		// elapses. The 3s countdown lives on the frontend; awaitSummaryConfirm only
		// backstops an absent/old client.
		deps.AwaitSummaryConfirm = func(ctx context.Context, cacheKey, original string) (string, bool) {
			o.hub.Send(sid, transport.Event{
				Type:      transport.EventSummaryConfirm,
				SessionID: sid,
				Data:      map[string]any{"cacheKey": cacheKey},
			})
			return o.awaitSummaryConfirm(ctx, sid, cacheKey, original)
		}
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

	// Tool-execution callback: records every tool that ACTUALLY executes
	// (authoritative count, see toolExecTracker) and surfaces tool_call/tool_result
	// events so the frontend can show the action trajectory.
	tracker := &toolExecTracker{}
	toolCB := o.toolCallbackHandler(sessionID, tracker)

	baseMsgs := w.Messages()
	var (
		reply     string
		cancelled bool
	)
	var serr error
	// Self-correcting retry loop: a weak model sometimes "confirms" an action in
	// prose without ever calling the tool (looksLikeFakeExecAck). When that
	// happens we re-run once with a stern correction appended, instead of leaving
	// the workspace empty. maxAttempts=2 bounds this to a single correction so we
	// never amplify latency/cost (see fakeack.go).
	//
	// "Did this attempt call a tool?" is judged from the tracker (real executions),
	// NOT from the output stream: eino never surfaces the model's tool_calls message
	// at END, so a stream-derived count is always zero and would misfire the retry
	// — re-running a tool that already ran and producing duplicate artifacts.
	const maxAttempts = 2
	attemptMsgs := baseMsgs
	prevExec := 0
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		reply, serr = o.streamOnce(ctx, ra, sessionID, attemptMsgs, toolCB)
		if serr != nil {
			o.emitTurnEnd(sessionID, turnEndInfo{replyEmpty: true})
			return "", fmt.Errorf("agent stream: %w", serr)
		}
		if ctx.Err() != nil {
			break // cancelled: stop retrying
		}
		attemptExec := len(tracker.snapshot()) - prevExec // tools this attempt ran
		prevExec += attemptExec
		if !shouldRetryFakeAck(attempt, maxAttempts, attemptExec, reply) {
			break
		}
		// This attempt streamed a fake-exec ack to the frontend already (done:false
		// increments). Tell the frontend to discard those increments before we
		// re-run, otherwise the retry's output would append to the stale fake text
		// and surface as duplicated confirmation prose (the exact bug we fix).
		o.emit(sessionID, transport.Event{Type: transport.EventTurnReset, SessionID: sessionID})
		// Append the faked ack + a stern correction so the next attempt actually
		// calls the tool rather than repeating the prose confirmation.
		log.Printf("agent: session=%s fake-exec ack detected (attempt %d), self-correcting", sessionID, attempt)
		applog.From(ctx).Warn().
			Str("event", "fakeack.retry").
			Int("attempt", attempt).
			Int("attempt_exec", attemptExec).
			Str("intent", intentLabel(hint)).
			Msg("fake-exec ack detected, self-correcting")
		attemptMsgs = append(append([]*schema.Message{}, attemptMsgs...),
			schema.AssistantMessage(reply, nil),
			schema.UserMessage(fakeAckCorrection),
		)
	}
	cancelled = ctx.Err() != nil
	// Authoritative tool-execution list for the whole turn (drives window structure,
	// remediation, and turn_end metadata).
	turnCalls := tracker.snapshot()

	// Diagnostic log: captures model id, compression state, and whether the
	// window still contains a complete tool exchange (few-shot anchor). Use
	// baseMsgs (what the model actually saw) to audit the post-compression state.
	turnModelID := turnModel.cfg.Model
	if turnModelID == "" {
		turnModelID = turnModel.cfg.Provider
	}
	log.Printf("agent: session=%s turn done model=%s compressed=%t has_tool_exchange=%t toolCalls=%d replyLen=%d capsule=%t cancelled=%t",
		sessionID, turnModelID, w.Compressed(), recentHasToolExchange(baseMsgs),
		len(turnCalls), len(reply), capsuleAsked, cancelled)
	hasToolExchange := recentHasToolExchange(baseMsgs)
	applog.From(ctx).Info().
		Str("event", "turn.done").
		Str("model", turnModelID).
		Bool("compressed", w.Compressed()).
		Bool("has_tool_exchange", hasToolExchange).
		Int("tool_calls", len(turnCalls)).
		Int("reply_len", len(reply)).
		Bool("capsule", capsuleAsked).
		Bool("cancelled", cancelled).
		Str("intent", intentLabel(hint)).
		Msg("turn done")
	// Explicit "the model should have called a tool but didn't" verdict: the
	// single most useful signal when chasing tool-not-executed reports.
	if len(turnCalls) == 0 && !cancelled && hint.Whitelisted {
		applog.From(ctx).Warn().
			Str("event", "tool.zero_exec").
			Str("intent", intentLabel(hint)).
			Float64("confidence", hint.Confidence).
			Bool("capsule", capsuleAsked).
			Int("reply_len", len(reply)).
			Msg("zero real tool executions despite tool-expecting intent")
	}

	if cancelled {
		// Interrupted mid-turn: persist whatever text was produced (keeps context
		// coherent for the next turn) but tell the frontend the turn was
		// cancelled so it can close the loading state without a stray bubble. A
		// cancelled turn's tool calls may not have executed, so we deliberately do
		// NOT fabricate a tool-call structure here — just the prose, best-effort.
		w.Append(schema.AssistantMessage(reply, nil))
		o.persist(sessionID, "user", userText)
		if strings.TrimSpace(reply) != "" {
			o.persist(sessionID, "assistant", reply)
		}
		o.emitTurnEnd(sessionID, turnEndInfo{toolCalls: len(turnCalls), hasCapsule: capsuleAsked, replyEmpty: strings.TrimSpace(reply) == "", cancelled: true})
		return reply, nil
	}

	replyEmpty := strings.TrimSpace(reply) == ""
	// Remediation fallback: the model still made zero tool calls after retries.
	// Use the deterministic pre-classification to recover instead of leaving the
	// turn empty or wrong (see remediationAction).
	switch remediationAction(len(turnCalls), cancelled, capsuleAsked, replyEmpty, reply, hint) {
	case remediateClarify:
		applog.From(ctx).Warn().Str("event", "remediation.decision").Str("decision", "clarify").Str("intent", intentLabel(hint)).Msg("remediation: clarify")
		q, opts := remediationClarify(hint, o.LastProduced(sessionID))
		clarify(q, opts)
	case remediateRefuse:
		applog.From(ctx).Warn().Str("event", "remediation.decision").Str("decision", "refuse").Str("intent", intentLabel(hint)).Msg("remediation: refuse")
		reply = RefusalMessage()
		replyEmpty = false
	case remediateHonestFail:
		// A fake-exec ack survived the retry budget: the model promised work but
		// never called a tool. Do not let the false confirmation pose as a real
		// reply — replace it with honest feedback (see fakeack.go).
		log.Printf("agent: session=%s fake-exec ack survived retries, honest-fail remediation", sessionID)
		applog.From(ctx).Warn().Str("event", "remediation.decision").Str("decision", "honest_fail").Str("intent", intentLabel(hint)).Msg("fake-exec ack survived retries, honest-fail remediation")
		reply = honestFailMessage
		replyEmpty = false
	}
	// Record this turn into the window with its REAL structure: when the model
	// called tools, append the assistant{tool_calls} message + one tool result per
	// call (not a bare text ack). This keeps the live window — and, via tool_refs,
	// the restored window — a valid tool exchange so the model keeps seeing that
	// past turns called tools (the reverse-few-shot bug, see restoreLocked).
	for _, m := range turnAssistantMessages(reply, turnCalls) {
		w.Append(m)
	}
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
	// Only text + a compact tool-call note are stored (large tool payloads stay out
	// of the DB, mirroring D3); the note lets restore rebuild the tool structure.
	o.persist(sessionID, "user", userText)
	o.persistAssistant(sessionID, reply, turnCalls)
	o.emitTurnEnd(sessionID, turnEndInfo{toolCalls: len(turnCalls), hasCapsule: capsuleAsked, replyEmpty: replyEmpty})
	// Proactive follow-up: when tools were called (workspace changed) and no
	// clarify capsule is pending, suggest what the user could do next (T16).
	if len(turnCalls) > 0 && !capsuleAsked && !replyEmpty {
		o.emitFollowUp(sessionID)
	}
	return reply, nil
}

// streamOnce runs one pass of the react agent over msgs, consuming the stream and
// emitting reasoning / incremental message events as they arrive (this drives the
// typewriter UI). It returns the accumulated reply text. Tool calls are NOT
// derived from the stream here: eino routes either the final model text or a
// ReturnDirectly tool result to END, so the assistant message carrying tool_calls
// never appears in this terminal stream — counting it would report zero even when
// a tool really ran. The authoritative tool-execution record comes from the
// tool-node callbacks (toolExecTracker), so tool_call events and the turn's tool
// list are produced there, not here. The caller decides whether the turn is final
// and emits the terminal done:true frame; streamOnce never emits one.
func (o *Orchestrator) streamOnce(ctx context.Context, ra *react.Agent, sessionID string, msgs []*schema.Message, toolCB callbacks.Handler) (string, error) {
	start := time.Now()
	stream, err := ra.Stream(ctx, msgs, agentOption(toolCB))
	if err != nil {
		return "", err
	}
	defer stream.Close()

	var sb strings.Builder
	chunks := 0
	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err != io.EOF {
				// Surface the real upstream/transport error instead of silently
				// ending the turn with an empty reply (root-cause diagnostics).
				log.Printf("agent: session=%s stream recv error after %d chunks: %v", sessionID, chunks, err)
				applog.From(ctx).Error().
					Str("event", "stream.recv_error").
					Int("chunks", chunks).
					Err(err).
					Msg("stream recv error")
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
		if chunk.Content != "" {
			sb.WriteString(chunk.Content)
			o.emit(sessionID, transport.Event{
				Type:      transport.EventMessage,
				SessionID: sessionID,
				Data:      map[string]any{"text": chunk.Content, "done": false},
			})
		}
	}
	log.Printf("agent: session=%s stream attempt done replyLen=%d chunks=%d", sessionID, sb.Len(), chunks)
	applog.From(ctx).Info().
		Str("event", "model.response").
		Int("chunks", chunks).
		Int("reply_len", sb.Len()).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("model stream attempt done")
	return sb.String(), nil
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

// turnToolCall is one tool invocation observed during a turn, captured so the
// turn can be recorded with its real assistant{tool_calls}→tool structure.
type turnToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

// turnAssistantMessages builds the canonical window representation of a completed
// assistant turn. When the model called tools, it produces an assistant message
// carrying the tool_calls followed by one synthetic tool result per call (the
// raw result lives as an asset addressed by id — D3 — so a compact note suffices),
// then, if there was also prose, a trailing assistant text message. When no tool
// was called it is just the assistant text. This is the structure the provider
// requires (a role:"tool" message must follow an assistant with matching
// tool_calls) and the structure that keeps the model seeing that past turns used
// tools, instead of the bare-ack collapse that reverse-trained it to stop.
func turnAssistantMessages(reply string, calls []turnToolCall) []*schema.Message {
	if len(calls) == 0 {
		return []*schema.Message{schema.AssistantMessage(reply, nil)}
	}
	tcs := make([]schema.ToolCall, 0, len(calls))
	for _, c := range calls {
		tcs = append(tcs, schema.ToolCall{
			ID:       c.ID,
			Function: schema.FunctionCall{Name: c.Name, Arguments: c.Args},
		})
	}
	out := []*schema.Message{schema.AssistantMessage("", tcs)}
	for _, c := range calls {
		tm := schema.ToolMessage("["+c.Name+" 已执行]", c.ID)
		tm.ToolName = c.Name
		out = append(out, tm)
	}
	if strings.TrimSpace(reply) != "" {
		out = append(out, schema.AssistantMessage(reply, nil))
	}
	return out
}

// persistAssistant stores a completed assistant turn. When the turn called tools,
// the tool-call list is recorded (compactly, as JSON) in the message's ToolRefs
// column so restoreLocked can rebuild the assistant{tool_calls}→tool structure on
// restart; the visible prose (if any) goes in Content. A pure-text turn persists
// as before. Nothing is stored when there is neither prose nor a tool call.
func (o *Orchestrator) persistAssistant(sessionID, reply string, calls []turnToolCall) {
	if o.store == nil {
		return
	}
	if len(calls) == 0 {
		o.persist(sessionID, "assistant", reply)
		return
	}
	refs, err := json.Marshal(calls)
	if err != nil {
		log.Printf("agent: marshal tool refs session=%s failed: %v", sessionID, err)
		o.persist(sessionID, "assistant", reply) // degrade to text-only rather than lose the turn
		return
	}
	id := "msg"
	if o.newID != nil {
		id = o.newID("msg")
	}
	if err := o.store.InsertMessage(store.MessageRecord{
		ID:        id,
		SessionID: sessionID,
		Role:      "assistant",
		Content:   reply,
		ToolRefs:  string(refs),
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		log.Printf("agent: persist assistant turn session=%s failed: %v", sessionID, err)
	}
}

// turnEndInfo carries the metadata sent with a turn_end event.
type turnEndInfo struct {
	toolCalls  int
	hasCapsule bool
	replyEmpty bool // no body text produced (frontend suppresses the empty bubble)
	cancelled  bool // the turn was interrupted by the user
}

// intentLabel renders an IntentHint as a compact log field: the matched
// whitelist labels joined by "/", or "none" when nothing matched. Used so trace
// logs carry the deterministic pre-classification verdict for each turn.
func intentLabel(hint IntentHint) string {
	if len(hint.Labels) == 0 {
		return "none"
	}
	return strings.Join(hint.Labels, "/")
}

// remediationClarify builds the fallback clarify question used when the model
// made no tool call but pre-classification says the intent is whitelisted yet a
// key parameter (the image to act on) is missing. The options steer the user
// toward supplying or generating an image so the next turn can proceed.
//
// lastProduced, when non-empty, is the session's most recent output asset: it is
// offered as the first option ("继续在上一张图上修改") so the user can one-click
// continue on it. This is a degraded path: the normal sticky-last-output flow
// annotates "[上次产物]" upstream so MissingKeyParam never fires and this
// function is not reached. It only triggers after a restart (lastProduced lost,
// rebuilt here from whatever the caller still tracks) — clarify-recent-context.
func remediationClarify(hint IntentHint, lastProduced string) (string, []ClarifyOption) {
	intent := "这个操作"
	if len(hint.Labels) > 0 {
		intent = "「" + strings.Join(hint.Labels, "/") + "」"
	}
	q := "我可以帮你" + intent + "，但还不清楚要操作哪张图。请告诉我，或先准备一张图："
	opts := make([]ClarifyOption, 0, 4)
	if lastProduced != "" {
		opts = append(opts, ClarifyOption{
			Label:        "继续在上一张图上修改",
			Value:        "[asset " + lastProduced + "] 用上一张产物来" + intent,
			EditableHint: "继续修改上一张图",
		})
	}
	opts = append(opts,
		ClarifyOption{Label: "上传/选中一张图", Value: "我先上传一张图，请用它来" + intent, EditableHint: "我要操作的是这张图"},
		ClarifyOption{Label: "用文字生成一张", Value: "先用文字帮我生成一张图，再" + intent, EditableHint: "帮我生成一张……的图"},
		ClarifyOption{Label: "搜一张参考图", Value: "先帮我搜一张参考图，再" + intent, EditableHint: "帮我搜一张……的图"},
	)
	return q, opts
}

// emitFollowUp sends a proactive follow-up suggestion after a productive turn.
func (o *Orchestrator) emitFollowUp(sessionID string) {
	// o.emit(sessionID, transport.Event{
	// 	Type:      transport.EventFollowUp,
	// 	SessionID: sessionID,
	// 	Data: map[string]any{
	// 		"message": "已完成！接下来想做什么？",
	// 		"options": []map[string]any{
	// 			{"label": "生成视频", "value": "帮我把刚才的图生成一段视频"},
	// 			{"label": "再换个风格", "value": "帮我再生成一版，换个风格"},
	// 			{"label": "切平台尺寸", "value": "帮我切成各平台的尺寸"},
	// 			{"label": "下载产物", "value": "下载刚才生成的产物"},
	// 		},
	// 	},
	// })
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

// degradeNotifier returns a once-guarded callback that, on first invocation,
// emits a turn_start carrying {streaming:false, degraded:true} so the frontend
// switches to the static (P2) wait fallback. The agent may attempt the model
// more than once per turn (fake-ack retry), so a sync.Once bounds the signal to
// a single emission; a repeat turn_start is treated idempotently by the client.
func (o *Orchestrator) degradeNotifier(sessionID string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			o.emit(sessionID, transport.Event{
				Type:      transport.EventTurnStart,
				SessionID: sessionID,
				Data:      map[string]any{"streaming": false, "degraded": true},
			})
		})
	}
}

// emit sends an event to the session's WS connections when a hub is present.
func (o *Orchestrator) emit(sessionID string, ev transport.Event) {
	if o.hub != nil {
		o.hub.Send(sessionID, ev)
	}
}

// agentOption wraps a callbacks handler as a react agent option.
// toolExecTracker records the tools that ACTUALLY executed during one turn,
// observed via the tool-node callbacks (OnStart/OnEnd). This is the authoritative
// count of tool usage — NOT the count of tool_calls seen in the agent's output
// stream. The two differ: eino's react agent routes either the final model text
// or a ReturnDirectly tool's result message to END, so the model's assistant
// message carrying tool_calls never appears in ra.Stream()'s terminal output.
// Counting stream chunks therefore reports zero tool calls even when a generation
// tool really ran, which previously misfired the fake-exec retry (re-running the
// tool → duplicate artifacts) and the honest-fail remediation. Observing the
// callbacks instead reflects what truly happened.
//
// Concurrency-safe: the react framework may dispatch parallel tool calls on
// separate goroutines.
type toolExecTracker struct {
	mu    sync.Mutex
	calls []turnToolCall
}

// record appends one observed tool execution. id may be empty if the framework
// did not expose a tool-call id in this context.
func (t *toolExecTracker) record(id, name, args string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, turnToolCall{ID: id, Name: name, Args: args})
}

// snapshot returns a copy of the executions observed so far.
func (t *toolExecTracker) snapshot() []turnToolCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]turnToolCall, len(t.calls))
	copy(out, t.calls)
	return out
}

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
	case "generate_icon", "generate_image_from_text":
		return "generate"
	case "search_images":
		return "search"
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

// toolCallbackHandler builds a handler that (1) records each tool that actually
// executes into tracker — the authoritative tool-usage count for this turn (see
// toolExecTracker) — and emits a tool_call event when it starts, and (2) emits a
// tool_result event when it finishes (success or error), so the UI can show the
// action trajectory and complete the action card.
func (o *Orchestrator) toolCallbackHandler(sessionID string, tracker *toolExecTracker) callbacks.Handler {
	return utilcb.NewHandlerHelper().Tool(&utilcb.ToolCallbackHandler{
		OnStart: func(ctx context.Context, info *callbacks.RunInfo, input *tool.CallbackInput) context.Context {
			name := ""
			if info != nil {
				name = info.Name
			}
			args := ""
			if input != nil {
				args = input.ArgumentsInJSON
			}
			id := compose.GetToolCallID(ctx)
			// Authoritative record of a real execution (drives retry / remediation /
			// window structure), independent of what the output stream reports.
			tracker.record(id, name, args)
			// Full, UNtruncated arguments to the trace log — this is the record you
			// reach for when chasing "tool not executed / executed with wrong args".
			// The frontend event below stays truncated (UI concern).
			applog.From(ctx).Info().
				Str("event", "tool.start").
				Str("tool", name).
				Str("tool_call_id", id).
				Str("args", args).
				Msg("tool start")
			o.emit(sessionID, transport.Event{
				Type:      transport.EventToolCall,
				SessionID: sessionID,
				Data: map[string]string{
					"id":        id,
					"name":      name,
					"arguments": truncate(args, 400),
				},
			})
			return ctx
		},
		OnEnd: func(ctx context.Context, info *callbacks.RunInfo, output *tool.CallbackOutput) context.Context {
			name := ""
			if info != nil {
				name = info.Name
			}
			summary := ""
			if output != nil {
				summary = truncate(output.Response, 200)
			}
			applog.From(ctx).Info().
				Str("event", "tool.end").
				Str("tool", name).
				Str("tool_call_id", compose.GetToolCallID(ctx)).
				Str("summary", summary).
				Msg("tool end")
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
			// Full error to the trace log; the frontend event stays truncated.
			applog.From(ctx).Error().
				Str("event", "tool.error").
				Str("tool", name).
				Str("tool_call_id", compose.GetToolCallID(ctx)).
				Err(err).
				Msg("tool error")
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
	// SystemTokens is the base cost of the system prompt alone. The frontend
	// uses this to display net conversation usage (total − system) so clearing
	// context shows 0% rather than ~19%.
	SystemTokens int `json:"systemTokens"`
}

// State returns the context window snapshot for a session.
func (o *Orchestrator) State(sessionID string) ContextState {
	w := o.window(sessionID)
	return ContextState{
		EstimatedTokens: w.EstimateTokens(),
		Budget:          o.budget,
		Compressed:      w.Compressed(),
		SystemTokens:    w.SystemTokens(),
	}
}
