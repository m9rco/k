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
