package layering

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"gameasset/internal/generation"
	"gameasset/internal/store"
	"gameasset/internal/vision"
)

// fakeDetector returns a fixed subject list.
type fakeDetector struct {
	subjects []vision.Subject
	err      error
}

func (f fakeDetector) Configured() bool { return true }
func (f fakeDetector) DetectSubjects(_ context.Context, _ []byte, _ string) ([]vision.Subject, error) {
	return f.subjects, f.err
}

// fakeGen records the params it was asked to start and immediately marks each
// spawned task done with a synthetic asset, so Split's await loop terminates fast.
type fakeGen struct {
	mu     sync.Mutex
	st     *store.Store
	sess   string
	n      int
	starts []generation.Slots
	failBG bool // when true, the fill_background task is marked failed
}

func (g *fakeGen) Start(_ context.Context, p generation.GenerateParams) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	taskID := "t" + strconv.Itoa(g.n)
	g.starts = append(g.starts, p.Slots)
	now := time.Now().UTC()
	status := "done"
	assetID := taskID + "-asset"
	if g.failBG && p.Slots.Kind == generation.EditBackgroundFill {
		status, assetID = "failed", ""
	}
	_ = g.st.InsertTask(store.TaskRecord{ID: taskID, SessionID: g.sess, Kind: "generate", Status: status, AssetID: assetID, CreatedAt: now, UpdatedAt: now})
	return taskID, nil
}

func newTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC()
	_ = st.UpsertSession(store.SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	srcPath := filepath.Join(dir, "src.png")
	_ = os.WriteFile(srcPath, []byte("notreallypng"), 0o644)
	_ = st.InsertAsset(store.AssetRecord{ID: "src", SessionID: "s", Kind: "generated", Path: srcPath, Mime: "image/png", Width: 900, Height: 600, CreatedAt: now})
	return st, dir
}

func newSvc(t *testing.T, det detector, gen generator, st *store.Store) *Service {
	s := NewService(det, gen, st)
	s.awaitTimeout = 5 * time.Second
	s.poll = 5 * time.Millisecond
	return s
}

func TestSplitProducesBackgroundAndSubjectLayers(t *testing.T) {
	st, _ := newTestStore(t)
	det := fakeDetector{subjects: []vision.Subject{{Desc: "战士"}, {Desc: "LOGO"}}}
	gen := &fakeGen{st: st, sess: "s"}
	res, err := newSvc(t, det, gen, st).Split(context.Background(), "s", "src")
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 900 || res.Height != 600 {
		t.Errorf("canvas size must inherit source 900x600, got %dx%d", res.Width, res.Height)
	}
	if len(res.Layers) != 3 {
		t.Fatalf("want 3 layers (bg + 2 subjects), got %d", len(res.Layers))
	}
	if res.Layers[0].Role != RoleBackground {
		t.Errorf("first layer must be the background, got %q", res.Layers[0].Role)
	}
	if res.Layers[1].Role != RoleSubject || res.Layers[1].Desc != "战士" {
		t.Errorf("unexpected subject layer %+v", res.Layers[1])
	}
	// Verify the intents dispatched: one fill_background + one extract_layer per subject.
	var bg, cut int
	for _, sl := range gen.starts {
		switch sl.Kind {
		case generation.EditBackgroundFill:
			bg++
		case generation.EditExtractLayer:
			cut++
		}
	}
	if bg != 1 || cut != 2 {
		t.Errorf("want 1 fill_background + 2 extract_layer, got bg=%d cut=%d", bg, cut)
	}
}

func TestSplitFailsWhenNoSubjects(t *testing.T) {
	st, _ := newTestStore(t)
	gen := &fakeGen{st: st, sess: "s"}
	_, err := newSvc(t, fakeDetector{subjects: nil}, gen, st).Split(context.Background(), "s", "src")
	if err == nil {
		t.Fatal("expected error when no subjects detected")
	}
}

func TestSplitFallsBackToSourceBackgroundWhenFillFails(t *testing.T) {
	st, _ := newTestStore(t)
	det := fakeDetector{subjects: []vision.Subject{{Desc: "战士"}}}
	gen := &fakeGen{st: st, sess: "s", failBG: true}
	res, err := newSvc(t, det, gen, st).Split(context.Background(), "s", "src")
	if err != nil {
		t.Fatalf("split must not fail when only the background fill fails: %v", err)
	}
	if len(res.Layers) < 2 {
		t.Fatalf("want background + ≥1 subject, got %d layers", len(res.Layers))
	}
	if res.Layers[0].Role != RoleBackground {
		t.Fatalf("first layer must be the background, got %q", res.Layers[0].Role)
	}
	// Background falls back to the ORIGINAL source asset id.
	if res.Layers[0].AssetID != "src" {
		t.Errorf("failed background must fall back to source asset 'src', got %q", res.Layers[0].AssetID)
	}
}

func TestSplitUnconfigured(t *testing.T) {
	st, _ := newTestStore(t)
	s := NewService(nil, &fakeGen{st: st, sess: "s"}, st)
	if s.Configured() {
		t.Fatal("service with nil detector must not be Configured()")
	}
	if _, err := s.Split(context.Background(), "s", "src"); err == nil {
		t.Fatal("expected unavailable error")
	}
}
