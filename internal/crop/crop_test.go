package crop

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gameasset/internal/config"
	"gameasset/internal/store"
)

// makePNG builds a w×h solid-color PNG for testing.
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCoverCropExactDimensions(t *testing.T) {
	// Landscape source cropped to portrait target.
	data := makePNG(t, 1600, 900)
	res, err := CropBytes(data, 1080, 1920)
	if err != nil {
		t.Fatalf("CropBytes: %v", err)
	}
	if res.Width != 1080 || res.Height != 1920 {
		t.Fatalf("dimensions = %dx%d, want 1080x1920", res.Width, res.Height)
	}
	// Verify the decoded output really is that size.
	img, _, err := image.Decode(bytes.NewReader(res.Data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 1080 || img.Bounds().Dy() != 1920 {
		t.Errorf("decoded size = %v", img.Bounds())
	}
}

func TestCoverCropPortraitToLandscape(t *testing.T) {
	data := makePNG(t, 600, 1200)
	res, err := CropBytes(data, 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 1920 || res.Height != 1080 {
		t.Errorf("got %dx%d", res.Width, res.Height)
	}
}

func TestCoverCropInvalidTarget(t *testing.T) {
	data := makePNG(t, 100, 100)
	if _, err := CropBytes(data, 0, 100); err == nil {
		t.Error("expected error for zero width")
	}
}

func newCropService(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	channels := []config.Channel{{
		ID:    "test",
		Name:  "Test",
		Group: "Universal",
		AssetTypes: []config.AssetType{{
			Type: "general",
			Name: "通用",
			Sizes: []config.Size{
				{ID: "test.square", Name: "Square", Width: 200, Height: 200, Orientation: "square", Producible: true},
				{ID: "test.wide", Name: "Wide", Width: 400, Height: 100, Orientation: "landscape", Producible: true},
				{ID: "test.video", Name: "Video", Width: 1280, Height: 720, Orientation: "landscape", Producible: false},
			},
		}},
	}}
	var counter int
	gen := func() string { counter++; return "crop" + strconv.Itoa(counter) }
	svc := NewService(channels, filepath.Join(dir, "assets"), st, gen)
	return svc, st, dir
}

func TestCropToSizesProducesAssets(t *testing.T) {
	svc, st, dir := newCropService(t)
	now := time.Now().UTC()
	if err := st.UpsertSession(store.SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	// Write a source asset file and record.
	srcPath := filepath.Join(dir, "src.png")
	if err := os.WriteFile(srcPath, makePNG(t, 800, 600), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertAsset(store.AssetRecord{ID: "src", SessionID: "s", Kind: "upload", Path: srcPath, Mime: "image/png", Width: 800, Height: 600, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	results, err := svc.CropToSizes("s", "src", []string{"test.square", "test.wide"})
	if err != nil {
		t.Fatalf("CropToSizes: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if _, err := os.Stat(r.Path); err != nil {
			t.Errorf("crop file missing: %v", err)
		}
		if r.SizeID == "" || r.ChannelID != "test" {
			t.Errorf("result missing id/channel labels: %+v", r)
		}
		if r.Bytes <= 0 {
			t.Errorf("result missing byte size: %+v", r)
		}
		// Each product must be persisted and readable by the owning session.
		a, err := st.GetAsset("s", r.AssetID)
		if err != nil || a == nil {
			t.Errorf("crop asset not persisted: %v", err)
		}
		if a != nil && a.Kind != "cropped" {
			t.Errorf("kind = %q, want cropped", a.Kind)
		}
	}
}

func TestCropToSizesUnknownSourceFails(t *testing.T) {
	svc, st, _ := newCropService(t)
	now := time.Now().UTC()
	_ = st.UpsertSession(store.SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	if _, err := svc.CropToSizes("s", "missing", []string{"test.square"}); err == nil {
		t.Error("expected error for missing source asset")
	}
}

func TestResolveSizeIDsErrors(t *testing.T) {
	svc, _, _ := newCropService(t)

	// Unknown id is a hard error.
	if _, err := svc.resolveSizeIDs([]string{"nope"}); err == nil {
		t.Error("expected error for unknown size id")
	}
	// Non-producible size is rejected.
	if _, err := svc.resolveSizeIDs([]string{"test.video"}); err == nil {
		t.Error("expected error for non-producible size")
	}
	// Empty request is rejected.
	if _, err := svc.resolveSizeIDs(nil); err == nil {
		t.Error("expected error for empty id list")
	}
	// Valid ids resolve in order.
	refs, err := svc.resolveSizeIDs([]string{"test.square", "test.wide"})
	if err != nil {
		t.Fatalf("resolveSizeIDs: %v", err)
	}
	if len(refs) != 2 || refs[0].size.ID != "test.square" || refs[1].channelID != "test" {
		t.Errorf("unexpected refs: %+v", refs)
	}
}
