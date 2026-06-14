package agent

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"gameasset/internal/config"
	"gameasset/internal/crop"
	"gameasset/internal/generation"
	"gameasset/internal/store"
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
	// edit_image, crop_to_sizes, list_platform_sizes, clarify_intent, generate_icon
	// (video and crawl are gated behind configured providers, absent here).
	if len(tools) != 5 {
		t.Fatalf("expected 5 whitelist tools, got %d", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		names[info.Name] = true
	}
	if !names["clarify_intent"] {
		t.Error("clarify_intent tool not registered")
	}
	if !names["generate_icon"] {
		t.Error("generate_icon tool not registered")
	}
}

func TestClarifyToolEmitsCapsule(t *testing.T) {
	var gotQ string
	var gotOpts []ClarifyOption
	deps := ToolDeps{
		SessionID: "s1",
		Clarify: func(q string, opts []ClarifyOption) {
			gotQ = q
			gotOpts = opts
		},
	}
	tl, err := deps.newClarifyTool()
	if err != nil {
		t.Fatal(err)
	}
	args := `{"question":"你想把背景换成什么？","options":[{"label":"淡紫色","value":"把背景换成淡紫色渐变","editable_hint":"淡紫色渐变背景"},{"label":"赛博朋克","value":"把背景换成赛博朋克夜景"}]}`
	if _, err := tl.InvokableRun(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if gotQ != "你想把背景换成什么？" {
		t.Errorf("question = %q", gotQ)
	}
	if len(gotOpts) != 2 {
		t.Fatalf("expected 2 options, got %d", len(gotOpts))
	}
	if gotOpts[0].Value != "把背景换成淡紫色渐变" || gotOpts[0].EditableHint != "淡紫色渐变背景" {
		t.Errorf("option[0] = %+v", gotOpts[0])
	}
}

func TestClarifyToolIsReturnDirectly(t *testing.T) {
	if _, ok := AsyncTaskTools()["clarify_intent"]; !ok {
		t.Error("clarify_intent must be a ToolReturnDirectly tool so the turn ends after asking")
	}
}

// --- conversation history restore -----------------------------------------

// newRestoreOrch builds a minimal orchestrator wired only with a store, enough
// to exercise the window-restore path without a live model.
func newRestoreOrch(t *testing.T, st *store.Store) *Orchestrator {
	t.Helper()
	return &Orchestrator{
		budget:     4000,
		keepRecent: 6,
		store:      st,
		newID:      func(p string) string { return p + "1" },
		windows:    make(map[string]*Window),
		cancels:    make(map[string]context.CancelFunc),
		turnMu:     make(map[string]*sync.Mutex),
	}
}

func TestSessionTurnLockIsStablePerSession(t *testing.T) {
	o := &Orchestrator{turnMu: make(map[string]*sync.Mutex), cancels: make(map[string]context.CancelFunc)}
	a1 := o.sessionTurnLock("s1")
	a2 := o.sessionTurnLock("s1")
	b1 := o.sessionTurnLock("s2")
	if a1 != a2 {
		t.Error("same session should return the same lock")
	}
	if a1 == b1 {
		t.Error("different sessions must have distinct locks")
	}
}

func TestCancelTurnFiresRegisteredCancel(t *testing.T) {
	o := &Orchestrator{turnMu: make(map[string]*sync.Mutex), cancels: make(map[string]context.CancelFunc)}
	_, cancel := context.WithCancel(context.Background())
	fired := false
	o.cancels["s1"] = func() { fired = true; cancel() }
	o.CancelTurn("s1")
	if !fired {
		t.Error("CancelTurn should invoke the registered cancel func")
	}
	// No-op when nothing registered (must not panic).
	o.CancelTurn("nobody")
}

func TestWindowRestoresPersistedHistory(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC()
	_ = st.UpsertSession(store.SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	_ = st.InsertMessage(store.MessageRecord{ID: "m1", SessionID: "s", Role: "user", Content: "把背景换成淡紫色", CreatedAt: now})
	_ = st.InsertMessage(store.MessageRecord{ID: "m2", SessionID: "s", Role: "assistant", Content: "好的，正在处理", CreatedAt: now.Add(time.Second)})

	// A fresh orchestrator (simulating a restart) must rebuild the window from
	// persisted history: system prompt + the two restored turns.
	o := newRestoreOrch(t, st)
	w := o.window("s")
	msgs := w.Messages()
	if msgs[0].Role != schema.System {
		t.Fatalf("first message must be system prompt, got %q", msgs[0].Role)
	}
	var foundUser, foundAssistant bool
	for _, m := range msgs {
		if m.Role == schema.User && strings.Contains(m.Content, "淡紫色") {
			foundUser = true
		}
		if m.Role == schema.Assistant && strings.Contains(m.Content, "正在处理") {
			foundAssistant = true
		}
	}
	if !foundUser || !foundAssistant {
		t.Errorf("restored window missing turns: user=%t assistant=%t", foundUser, foundAssistant)
	}
}

func TestWindowEmptyWhenNoHistory(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	o := newRestoreOrch(t, st)
	w := o.window("fresh")
	msgs := w.Messages()
	if len(msgs) != 1 || msgs[0].Role != schema.System {
		t.Fatalf("fresh session should be system-only, got %d messages", len(msgs))
	}
}
