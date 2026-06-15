package agent

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gameasset/internal/config"
	"gameasset/internal/crop"
	"gameasset/internal/generation"
	"gameasset/internal/store"
	"gameasset/internal/transport"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newAdaptDeps wires a real generation service + crop service over the project
// channel catalog so the adapt tool exercises true routing. The source is a
// 1920×1080 landscape image so a 16:9 target takes the crop fast path (no
// provider call needed).
func newAdaptDeps(t *testing.T) (ToolDeps, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	// Build a deterministic catalog (config.Load can't find configs/channels.json
	// from the package test dir). One 16:9 producible size for the crop fast path.
	cfg.Channels = []config.Channel{{
		ID: "ch", Name: "TestCh", Group: "外渠",
		AssetTypes: []config.AssetType{{
			Type: "screenshot", Name: "截图",
			Sizes: []config.Size{
				{ID: "ch.landscape.1280x720", Name: "横版 16:9", Width: 1280, Height: 720, Orientation: "landscape", Producible: true},
			},
		}},
	}}
	var n int
	idFn := func(p string) string { n++; return p + strconv.Itoa(n) }
	cropSvc := crop.NewService(cfg.Channels, filepath.Join(dir, "assets"), st, func() string { n++; return "crop" + strconv.Itoa(n) })
	// A failover generator with a nil-ish provider; the crop fast path won't call it.
	genSvc := generation.NewService(nil, st, transport.NewTaskBroker(), filepath.Join(dir, "assets"), idFn)
	genSvc.SetCropper(cropSvc)

	now := time.Now().UTC()
	_ = st.UpsertSession(store.SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	src := filepath.Join(dir, "assets", "src.png")
	_ = os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	writePNG(t, src, 1920, 1080)
	_ = st.InsertAsset(store.AssetRecord{ID: "src", SessionID: "s", Kind: "upload", Path: src, Mime: "image/png", Width: 1920, Height: 1080, CreatedAt: now})

	return ToolDeps{Generation: genSvc, Crop: cropSvc, Store: st, SessionID: "s", dedup: newTurnCallGuard()}, st
}

// TestAdaptToolCropFastPath verifies a 16:9 source adapted to a 16:9 target
// takes the deterministic crop path (asset returned immediately, no task).
func TestAdaptToolCropFastPath(t *testing.T) {
	deps, _ := newAdaptDeps(t)
	at, err := deps.newAdaptTool()
	if err != nil {
		t.Fatal(err)
	}
	out, err := at.InvokableRun(context.Background(), `{"source_asset_id":"src","size_ids":["ch.landscape.1280x720"]}`)
	if err != nil {
		t.Fatalf("adapt call: %v", err)
	}
	if out == "" {
		t.Fatal("expected a non-empty acknowledgment")
	}
}

// TestAdaptToolDedupSameTurn verifies the same (source, sizes) call twice in one
// turn starts the work once and suppresses the duplicate (empty ack).
func TestAdaptToolDedupSameTurn(t *testing.T) {
	deps, st := newAdaptDeps(t)
	at, err := deps.newAdaptTool()
	if err != nil {
		t.Fatal(err)
	}
	args := `{"source_asset_id":"src","size_ids":["ch.landscape.1280x720"]}`
	if _, err := at.InvokableRun(context.Background(), args); err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := at.InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if second != "" {
		t.Errorf("duplicate same-turn call should yield empty ack, got %q", second)
	}
	// Exactly one crop product persisted (the duplicate was suppressed).
	assets, _ := st.ListAssets("s")
	cropped := 0
	for _, a := range assets {
		if a.Kind == "cropped" {
			cropped++
		}
	}
	if cropped != 1 {
		t.Errorf("expected exactly 1 cropped product, got %d", cropped)
	}
}

// TestAdaptToolUnknownSizeErrors verifies a bad size id surfaces an error.
func TestAdaptToolUnknownSizeErrors(t *testing.T) {
	deps, _ := newAdaptDeps(t)
	at, err := deps.newAdaptTool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := at.InvokableRun(context.Background(), `{"source_asset_id":"src","size_ids":["nope.bad.id"]}`); err == nil {
		t.Error("expected error for unknown size id")
	}
}
