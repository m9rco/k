package agent

import (
	"context"
	"encoding/json"
	"fmt"
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
	for _, want := range []string{"不调用任何工具", "ignore previous instructions", "暂未配置"} {
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
	// edit_image, crop_to_sizes, list_platform_sizes, clarify_intent, adapt_to_platform,
	// generate_variants, extract_layer (the last two gated on Generation, present here).
	// generate_icon is currently disabled; video/crawl/copywriting/overlay are gated
	// behind configured providers (all absent here).
	if len(tools) != 7 {
		t.Fatalf("expected 7 whitelist tools, got %d", len(tools))
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
	if !names["adapt_to_platform"] {
		t.Error("adapt_to_platform tool not registered")
	}
	if !names["generate_variants"] {
		t.Error("generate_variants tool not registered")
	}
	if !names["extract_layer"] {
		t.Error("extract_layer tool not registered")
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

// TestVariantsToolIsAsync guards that generate_variants stays in AsyncTaskTools:
// its batch result must never feed back to the model (which would let the model
// fabricate variant outputs before the async tasks actually complete).
func TestVariantsToolIsAsync(t *testing.T) {
	if _, ok := AsyncTaskTools()["generate_variants"]; !ok {
		t.Error("generate_variants must be in AsyncTaskTools so the batch result never feeds back to the model")
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

// --- tool-call history persistence / rebuild (reverse-few-shot fix) -------

// TestOpenAIBodySerializesAssistantToolCalls guards the core wire-level bug: a
// historical assistant message carrying tool_calls must serialize them, otherwise
// the following role:"tool" message is orphaned and the provider rejects the
// whole request (400) — and the model also stops seeing that past turns called
// tools.
func TestOpenAIBodySerializesAssistantToolCalls(t *testing.T) {
	m := &chatModel{cfg: config.ModelConfig{Provider: "openai", Model: "x"}}
	asst := schema.AssistantMessage("", []schema.ToolCall{{
		ID:       "call_1",
		Function: schema.FunctionCall{Name: "edit_image", Arguments: `{"source_asset_id":"a1"}`},
	}})
	toolMsg := schema.ToolMessage("[edit_image 已执行]", "call_1")
	body := m.openAIBody([]*schema.Message{
		schema.UserMessage("把背景改蓝"),
		asst,
		toolMsg,
	})
	// Round-trip through JSON to assert the exact shape the provider receives
	// (openAIBody builds an unexported anonymous struct we can't name here).
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	var decoded struct {
		Messages []struct {
			Role      string `json:"role"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	// Find the assistant message and assert it carries the tool call.
	var asstOK, toolOK bool
	for _, mm := range decoded.Messages {
		if mm.Role == "assistant" && len(mm.ToolCalls) == 1 {
			tc := mm.ToolCalls[0]
			if tc.ID == "call_1" && tc.Type == "function" && tc.Function.Name == "edit_image" && strings.Contains(tc.Function.Arguments, "a1") {
				asstOK = true
			}
		}
		if mm.Role == "tool" && mm.ToolCallID == "call_1" {
			toolOK = true
		}
	}
	if !asstOK {
		t.Error("assistant message must serialize its tool_calls (id/type/function)")
	}
	if !toolOK {
		t.Error("tool message must keep its tool_call_id so it pairs with the assistant call")
	}
}

// TestTurnAssistantMessagesStructure asserts the canonical window shape for a
// tool-calling turn: assistant{tool_calls} → tool result(s) → optional prose.
func TestTurnAssistantMessagesStructure(t *testing.T) {
	// No tools: just the assistant text.
	plain := turnAssistantMessages("你好", nil)
	if len(plain) != 1 || plain[0].Role != schema.Assistant || plain[0].Content != "你好" {
		t.Fatalf("plain turn = %d msgs, want 1 assistant text", len(plain))
	}

	// With tools: assistant(tool_calls) + tool result + trailing prose.
	calls := []turnToolCall{{ID: "c1", Name: "edit_image", Args: `{"x":1}`}}
	got := turnAssistantMessages("已开始处理", calls)
	if len(got) != 3 {
		t.Fatalf("tool turn = %d msgs, want 3 (assistant+tool+prose)", len(got))
	}
	if got[0].Role != schema.Assistant || len(got[0].ToolCalls) != 1 || got[0].ToolCalls[0].ID != "c1" {
		t.Errorf("msg[0] must be assistant carrying the tool call")
	}
	if got[1].Role != schema.Tool || got[1].ToolCallID != "c1" {
		t.Errorf("msg[1] must be a tool result paired to c1, got role=%q id=%q", got[1].Role, got[1].ToolCallID)
	}
	if got[2].Role != schema.Assistant || got[2].Content != "已开始处理" {
		t.Errorf("msg[2] must be the trailing prose")
	}

	// Tool call with NO prose: no trailing empty assistant text.
	noProse := turnAssistantMessages("", calls)
	if len(noProse) != 2 {
		t.Errorf("tool turn without prose = %d msgs, want 2 (assistant+tool)", len(noProse))
	}
}

// TestWindowRestoresToolCallStructure is the regression test for the reported
// bug: a restarted session whose history had tool-calling turns must rebuild
// them as real tool exchanges (not bare text acks), so the model keeps calling
// tools instead of mimicking a prose-only history.
func TestWindowRestoresToolCallStructure(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC()
	_ = st.UpsertSession(store.SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	_ = st.InsertMessage(store.MessageRecord{ID: "m1", SessionID: "s", Role: "user", Content: "把图1背景改蓝", CreatedAt: now})
	refs := `[{"id":"call_x","name":"edit_image","args":"{\"source_asset_id\":\"a1\"}"}]`
	_ = st.InsertMessage(store.MessageRecord{ID: "m2", SessionID: "s", Role: "assistant", Content: "已开始处理", ToolRefs: refs, CreatedAt: now.Add(time.Second)})

	o := newRestoreOrch(t, st)
	w := o.window("s")
	msgs := w.Messages()

	var asstWithCall, pairedTool bool
	for _, m := range msgs {
		if m.Role == schema.Assistant && len(m.ToolCalls) == 1 && m.ToolCalls[0].Function.Name == "edit_image" {
			asstWithCall = true
		}
		if m.Role == schema.Tool && m.ToolCallID == "call_x" {
			pairedTool = true
		}
	}
	if !asstWithCall {
		t.Error("restored history must rebuild the assistant message carrying tool_calls")
	}
	if !pairedTool {
		t.Error("restored history must rebuild a tool result paired to the call")
	}
}

// TestPersistAssistantWritesToolRefs asserts a tool-calling turn round-trips its
// tool list into the ToolRefs column (no schema change needed).
func TestPersistAssistantWritesToolRefs(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	o := newRestoreOrch(t, st)
	// Unique ids per insert: persistAssistant + the pure-text persist below would
	// otherwise collide on the fixed "msg1" primary key.
	var n int
	o.newID = func(p string) string { n++; return fmt.Sprintf("%s%d", p, n) }

	now := time.Now().UTC()
	_ = st.UpsertSession(store.SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	_ = st.UpsertSession(store.SessionRecord{ID: "s2", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})

	o.persistAssistant("s", "已开始处理", []turnToolCall{{ID: "c1", Name: "edit_image", Args: "{}"}})
	got, err := st.ListMessages("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 persisted message, got %d", len(got))
	}
	if got[0].ToolRefs == "" || !strings.Contains(got[0].ToolRefs, "edit_image") {
		t.Errorf("tool-calling turn must persist tool refs, got %q", got[0].ToolRefs)
	}
	// A pure-text turn must NOT write tool refs.
	o.persistAssistant("s2", "你好", nil)
	got2, _ := st.ListMessages("s2")
	if len(got2) != 1 || got2[0].ToolRefs != "" {
		t.Errorf("pure-text turn must not write tool refs, got %q", got2[0].ToolRefs)
	}
}

// TestWindowSummaryPreservesAssetAnchor verifies that when older turns are
// compressed away, the most recent edit lineage (source→output) is preserved as
// a structured "[最近编辑: …]" anchor in the summary (summary-asset-anchor).
func TestWindowSummaryPreservesAssetAnchor(t *testing.T) {
	w := NewWindow("SYS", 256, 2, nil)
	// An edit turn: user message carries the workspace map + last-produced label;
	// the assistant tool-call carries source_asset_id. After producing output a2,
	// the next turn's prefix annotates [上次产物: 图2] (=a2).
	w.Append(schema.UserMessage("[工作区: 图1=a1(生成)] [选中: 图1] 换个背景"))
	w.Append(&schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:       "c1",
			Function: schema.FunctionCall{Name: "edit_image", Arguments: `{"intent":"change_background","source_asset_id":"a1"}`},
		}},
	})
	w.Append(schema.UserMessage("[工作区: 图1=a1(生成), 图2=a2(生成)] [上次产物: 图2] 再换个角色"))
	// Pad with long turns to force compression of the early edit turn.
	long := strings.Repeat("赛博朋克城市夜景，霓虹灯，雨夜街道。", 30)
	for i := 0; i < 8; i++ {
		w.Append(schema.UserMessage(long))
	}
	if !w.Compressed() {
		t.Fatal("expected compression")
	}
	summary := w.Messages()[1].Content
	if !strings.Contains(summary, "[最近编辑:") {
		t.Fatalf("summary missing edit anchor: %q", summary)
	}
	if !strings.Contains(summary, "source=a1") || !strings.Contains(summary, "output=a2") {
		t.Errorf("anchor should carry source=a1 → output=a2, got %q", summary)
	}
	// Anchor must not be duplicated across repeated compressions.
	if strings.Count(summary, "[最近编辑:") != 1 {
		t.Errorf("anchor duplicated in %q", summary)
	}
}

// TestWindowSummaryNoAnchorWithoutEdits verifies a pure-text conversation yields
// no edit anchor (summary-asset-anchor: don't fabricate one).
func TestWindowSummaryNoAnchorWithoutEdits(t *testing.T) {
	w := NewWindow("SYS", 256, 2, nil)
	long := strings.Repeat("你好，我们来聊聊宣发素材的整体规划和思路吧。", 30)
	for i := 0; i < 8; i++ {
		w.Append(schema.UserMessage(long))
	}
	if !w.Compressed() {
		t.Fatal("expected compression")
	}
	if strings.Contains(w.Messages()[1].Content, "[最近编辑:") {
		t.Errorf("no edit turns should yield no anchor: %q", w.Messages()[1].Content)
	}
}

// TestOrchestratorLastProduced covers the per-session last-produced tracking
// used by sticky-last-output.
func TestOrchestratorLastProduced(t *testing.T) {
	o := &Orchestrator{lastProduced: make(map[string]string)}
	if got := o.LastProduced("s1"); got != "" {
		t.Errorf("empty session should yield empty, got %q", got)
	}
	o.SetLastProduced("s1", "asset_x")
	o.SetLastProduced("s2", "asset_y")
	if got := o.LastProduced("s1"); got != "asset_x" {
		t.Errorf("s1 last produced = %q, want asset_x", got)
	}
	if got := o.LastProduced("s2"); got != "asset_y" {
		t.Errorf("s2 last produced = %q, want asset_y", got)
	}
	// Latest write wins.
	o.SetLastProduced("s1", "asset_z")
	if got := o.LastProduced("s1"); got != "asset_z" {
		t.Errorf("s1 last produced should update to asset_z, got %q", got)
	}
}
