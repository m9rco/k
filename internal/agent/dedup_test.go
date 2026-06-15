package agent

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gameasset/internal/store"
	"gameasset/internal/transport"
	"gameasset/internal/video"
)

// stubVideoProvider is a minimal configured provider for the dedup test.
type stubVideoProvider struct{}

func (stubVideoProvider) Name() string     { return "stub" }
func (stubVideoProvider) Configured() bool { return true }
func (stubVideoProvider) Generate(_ context.Context, _ video.Request) (video.Output, error) {
	return video.Output{Data: []byte("MP4"), Mime: "video/mp4", Provider: "stub"}, nil
}

type stubVideoUploader struct{}

func (stubVideoUploader) Upload(_ context.Context, name string, _ []byte, _ string) (string, error) {
	return "https://public.example/" + name, nil
}

func newVideoDeps(t *testing.T) (ToolDeps, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var n int
	id := func(p string) string { n++; return p + strconv.Itoa(n) }
	svc := video.NewService(stubVideoProvider{}, st, transport.NewTaskBroker(), filepath.Join(dir, "assets"), id)
	svc.SetUploader(stubVideoUploader{})
	if !svc.Configured() {
		t.Fatal("video service should be configured")
	}
	now := time.Now().UTC()
	_ = st.UpsertSession(store.SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	src := filepath.Join(dir, "src.png")
	if err := os.WriteFile(src, []byte("PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = st.InsertAsset(store.AssetRecord{ID: "src", SessionID: "s", Kind: "generated", Path: src, Mime: "image/png", CreatedAt: now})
	return ToolDeps{Video: svc, Store: st, SessionID: "s", dedup: newTurnCallGuard()}, st
}

func countTasks(t *testing.T, st *store.Store) int {
	t.Helper()
	tasks, err := st.ListTasks("s")
	if err != nil {
		t.Fatal(err)
	}
	return len(tasks)
}

// drainTasks waits for every task to reach a terminal state so the async video
// goroutines stop writing into the temp dir before t.Cleanup removes it.
func drainTasks(t *testing.T, st *store.Store) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		tasks, _ := st.ListTasks("s")
		pending := false
		for _, tk := range tasks {
			if tk.Status != "done" && tk.Status != "failed" {
				pending = true
				break
			}
		}
		if !pending {
			return
		}
		select {
		case <-deadline:
			return
		case <-time.After(15 * time.Millisecond):
		}
	}
}

// TestVideoToolDedupSameTurn reproduces the reported bug: the model emits two
// identical image_to_video calls in one turn. The first starts a task and acks;
// the duplicate must be suppressed — no second task, empty ack (so the two acks
// don't concatenate into one bubble).
func TestVideoToolDedupSameTurn(t *testing.T) {
	deps, st := newVideoDeps(t)
	vt, err := deps.newVideoTool()
	if err != nil {
		t.Fatal(err)
	}
	args := `{"source_asset_id":"src","motion":"让角色走起来"}`

	first, err := vt.InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if first == "" {
		t.Fatal("first call should produce a non-empty acknowledgment")
	}

	second, err := vt.InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatalf("second (duplicate) call: %v", err)
	}
	if second != "" {
		t.Errorf("duplicate call should yield empty ack, got %q", second)
	}

	if n := countTasks(t, st); n != 1 {
		t.Errorf("expected exactly 1 video task started, got %d", n)
	}
	drainTasks(t, st)
}

// TestVideoToolDistinctCallsBothRun ensures the guard only collapses identical
// calls: two different motions in one turn both start a task.
func TestVideoToolDistinctCallsBothRun(t *testing.T) {
	deps, st := newVideoDeps(t)
	vt, err := deps.newVideoTool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vt.InvokableRun(context.Background(), `{"source_asset_id":"src","motion":"走"}`); err != nil {
		t.Fatalf("call A: %v", err)
	}
	if _, err := vt.InvokableRun(context.Background(), `{"source_asset_id":"src","motion":"跳"}`); err != nil {
		t.Fatalf("call B: %v", err)
	}
	if n := countTasks(t, st); n != 2 {
		t.Errorf("expected 2 distinct tasks, got %d", n)
	}
	drainTasks(t, st)
}

// TestVideoToolDedupIgnoresAwaitResult is the regression guard for the reported
// "生图发多个请求 + 确认话术重复两次" bug: the model emits the SAME generation
// twice in one turn, differing only in await_result (a chaining/delivery hint,
// not part of the produced artifact). The duplicate must still be suppressed —
// argSig drops await_result so both collapse to one signature.
func TestVideoToolDedupIgnoresAwaitResult(t *testing.T) {
	deps, st := newVideoDeps(t)
	vt, err := deps.newVideoTool()
	if err != nil {
		t.Fatal(err)
	}
	// Same source + motion; only await_result differs between the two calls.
	if _, err := vt.InvokableRun(context.Background(), `{"source_asset_id":"src","motion":"走","await_result":true}`); err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := vt.InvokableRun(context.Background(), `{"source_asset_id":"src","motion":"走","await_result":false}`)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if second != "" {
		t.Errorf("call differing only in await_result must be deduped (empty ack), got %q", second)
	}
	if n := countTasks(t, st); n != 1 {
		t.Errorf("expected exactly 1 task (await_result-only difference is a duplicate), got %d", n)
	}
	drainTasks(t, st)
}

func TestTurnCallGuardFirstSeen(t *testing.T) {
	g := newTurnCallGuard()
	if !g.firstSeen("a") {
		t.Error("first occurrence of a should be true")
	}
	if g.firstSeen("a") {
		t.Error("second occurrence of a should be false")
	}
	if !g.firstSeen("b") {
		t.Error("first occurrence of b should be true")
	}
	// nil guard never dedups.
	var nilGuard *turnCallGuard
	if !nilGuard.firstSeen("x") || !nilGuard.firstSeen("x") {
		t.Error("nil guard should always report first-seen")
	}
}

// TestEditToolMissingDescClarifies verifies edit_image surfaces a clarify capsule
// (not a turn-aborting error, not a doomed task) when the required per-intent
// description is empty. The tool must return without error and emit a question +
// editable options via the Clarify callback, so the user can answer in one turn.
func TestEditToolMissingDescClarifies(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{"change_background empty", `{"intent":"change_background","source_asset_id":"a1"}`},
		{"change_character empty", `{"intent":"change_character","source_asset_id":"a1"}`},
		{"add_character empty", `{"intent":"add_character","source_asset_id":"a1"}`},
		{"change_text empty", `{"intent":"change_text","source_asset_id":"a1"}`},
		{"whitespace-only desc", `{"intent":"change_background","source_asset_id":"a1","background_desc":"   "}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var gotQ string
			var gotOpts []ClarifyOption
			deps := ToolDeps{
				SessionID: "s",
				dedup:     newTurnCallGuard(),
				Clarify:   func(q string, o []ClarifyOption) { gotQ = q; gotOpts = o },
			}
			et, err := deps.newEditTool()
			if err != nil {
				t.Fatal(err)
			}
			out, err := et.InvokableRun(context.Background(), c.args)
			if err != nil {
				t.Fatalf("missing desc must NOT return a Go error (aborts the turn), got %v", err)
			}
			// Benign result -> no stray bubble (maps to empty via asyncMarshal).
			if out != "" {
				t.Errorf("expected empty acknowledgment for clarify path, got %q", out)
			}
			if gotQ == "" {
				t.Error("expected a clarify question to be surfaced")
			}
			if len(gotOpts) == 0 {
				t.Error("expected at least one clarify option")
			}
			for i, o := range gotOpts {
				if o.Label == "" || o.Value == "" {
					t.Errorf("option[%d] missing label/value: %+v", i, o)
				}
			}
		})
	}
}

// TestEditToolProceedsWithDesc verifies a present description does NOT trigger the
// clarify path (editMissingDesc returns false).
func TestEditToolProceedsWithDesc(t *testing.T) {
	if missing, _, _ := editMissingDesc(editArgs{Intent: "change_background", BackgroundDesc: "中国风"}); missing {
		t.Error("non-empty background_desc should not be flagged missing")
	}
	if missing, _, _ := editMissingDesc(editArgs{Intent: "add_character", CharacterDesc: "废土男性"}); missing {
		t.Error("non-empty character_desc should not be flagged missing")
	}
}
