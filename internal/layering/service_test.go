package layering

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gameasset/internal/composite"
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

// fakePersister records each persisted layer and hands back a synthetic asset id,
// so Split's crop→persist loop can be exercised without touching disk/store.
type fakePersister struct {
	mu     sync.Mutex
	n      int
	calls  [][]byte // persisted layer bytes, in order
	failAt int      // 1-based call index to fail (0 = never)
}

func (p *fakePersister) Persist(_ string, data []byte, _ []string, _ bool) (composite.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n++
	if p.failAt == p.n {
		return composite.Result{}, errFakePersist
	}
	p.calls = append(p.calls, data)
	w, h := 0, 0
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		w, h = cfg.Width, cfg.Height
	}
	return composite.Result{AssetID: "layer" + itoa(p.n), Width: w, Height: h, Mime: "image/png"}, nil
}

var errFakePersist = errFake("persist failed")

type errFake string

func (e errFake) Error() string { return string(e) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// newTestStore seeds a session + a REAL decodable source PNG so cropSubject can
// run against actual pixels.
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

	img := image.NewNRGBA(image.Rect(0, 0, 900, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 900; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(dir, "src.png")
	if err := os.WriteFile(srcPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = st.InsertAsset(store.AssetRecord{ID: "src", SessionID: "s", Kind: "generated", Path: srcPath, Mime: "image/png", Width: 900, Height: 600, CreatedAt: now})
	return st, dir
}

func TestSplitProducesOriginalBackgroundAndCroppedSubjects(t *testing.T) {
	st, _ := newTestStore(t)
	det := fakeDetector{subjects: []vision.Subject{
		{Desc: "战士", Box: vision.Box{X: 0.1, Y: 0.2, W: 0.3, H: 0.4}},
		{Desc: "主标题文案", Box: vision.Box{X: 0.5, Y: 0.05, W: 0.3, H: 0.1}},
	}}
	per := &fakePersister{}
	res, err := NewService(det, per, st).Split(context.Background(), "s", "src")
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 900 || res.Height != 600 {
		t.Errorf("canvas must inherit source 900x600, got %dx%d", res.Width, res.Height)
	}
	if len(res.Layers) != 3 {
		t.Fatalf("want 3 layers (original bg + 2 subjects), got %d", len(res.Layers))
	}
	// Background = the ORIGINAL source asset, full-frame box, no AI persist call.
	bg := res.Layers[0]
	if bg.Role != RoleBackground || bg.AssetID != "src" {
		t.Errorf("background must be the original source 'src', got role=%q id=%q", bg.Role, bg.AssetID)
	}
	if bg.Box != fullFrame {
		t.Errorf("background box must span the full frame, got %+v", bg.Box)
	}
	// Subjects carry their (padded) crop box and a persisted layer asset.
	if res.Layers[1].Role != RoleSubject || res.Layers[1].Desc != "战士" {
		t.Errorf("unexpected first subject layer %+v", res.Layers[1])
	}
	if res.Layers[1].AssetID != "layer1" {
		t.Errorf("first subject should be persisted layer1, got %q", res.Layers[1].AssetID)
	}
	if res.Layers[1].Box.W <= 0 || res.Layers[1].Box.X < 0 {
		t.Errorf("subject box looks wrong: %+v", res.Layers[1].Box)
	}
	// Only the 2 subjects were persisted — the background was NOT (it's the original).
	if len(per.calls) != 2 {
		t.Errorf("want exactly 2 persisted subject layers, got %d", len(per.calls))
	}
}

func TestSplitFailsWhenNoSubjects(t *testing.T) {
	st, _ := newTestStore(t)
	_, err := NewService(fakeDetector{subjects: nil}, &fakePersister{}, st).Split(context.Background(), "s", "src")
	if err == nil {
		t.Fatal("expected error when no subjects detected")
	}
}

func TestSplitSkipsFailedSubjectButKeepsOthers(t *testing.T) {
	st, _ := newTestStore(t)
	det := fakeDetector{subjects: []vision.Subject{
		{Desc: "A", Box: vision.Box{X: 0.1, Y: 0.1, W: 0.2, H: 0.2}},
		{Desc: "B", Box: vision.Box{X: 0.5, Y: 0.5, W: 0.2, H: 0.2}},
	}}
	per := &fakePersister{failAt: 1} // first subject's persist fails
	res, err := NewService(det, per, st).Split(context.Background(), "s", "src")
	if err != nil {
		t.Fatalf("one failed subject must not sink the split: %v", err)
	}
	// background + the second subject survive.
	if len(res.Layers) != 2 {
		t.Fatalf("want bg + 1 surviving subject, got %d layers", len(res.Layers))
	}
	if res.Layers[0].Role != RoleBackground || res.Layers[1].Desc != "B" {
		t.Errorf("unexpected layers: %+v", res.Layers)
	}
}

func TestSplitFailsWhenAllSubjectsFail(t *testing.T) {
	st, _ := newTestStore(t)
	det := fakeDetector{subjects: []vision.Subject{{Desc: "A", Box: vision.Box{X: 0.1, Y: 0.1, W: 0.2, H: 0.2}}}}
	per := &fakePersister{failAt: 1}
	if _, err := NewService(det, per, st).Split(context.Background(), "s", "src"); err == nil {
		t.Fatal("expected error when no subject layer could be produced (bg-only)")
	}
}

func TestSplitUnconfigured(t *testing.T) {
	st, _ := newTestStore(t)
	s := NewService(nil, &fakePersister{}, st)
	if s.Configured() {
		t.Fatal("service with nil detector must not be Configured()")
	}
	if _, err := s.Split(context.Background(), "s", "src"); err == nil {
		t.Fatal("expected unavailable error")
	}
}
