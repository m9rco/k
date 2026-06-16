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

// decodePNG decodes test bytes to an image for pixel inspection.
func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestCropModesExactDimensions(t *testing.T) {
	data := makePNG(t, 800, 600)
	cases := []struct {
		name string
		opts Options
	}{
		{"cover", Options{Mode: ModeCover}},
		{"contain", Options{Mode: ModeContain}},
		{"anchor-top", Options{Mode: ModeAnchor, Anchor: AnchorTop}},
		{"anchor-bottom-right", Options{Mode: ModeAnchor, Anchor: AnchorBottomRight}},
		{"rect", Options{Mode: ModeRect, Rect: &Rect{X: 0.25, Y: 0.25, W: 0.5, H: 0.5}}},
		{"default-empty", Options{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := CropBytesWithOptions(data, 300, 300, c.opts)
			if err != nil {
				t.Fatalf("CropBytesWithOptions: %v", err)
			}
			if res.Width != 300 || res.Height != 300 {
				t.Fatalf("dims = %dx%d, want 300x300", res.Width, res.Height)
			}
			img := decodePNG(t, res.Data)
			if img.Bounds().Dx() != 300 || img.Bounds().Dy() != 300 {
				t.Errorf("decoded size = %v", img.Bounds())
			}
		})
	}
}

func TestContainPadsBackground(t *testing.T) {
	// Wide source into a square box: top/bottom must be padded with the bg color.
	data := makePNG(t, 800, 200)
	red := color.RGBA{255, 0, 0, 255}
	res, err := CropBytesWithOptions(data, 400, 400, Options{Mode: ModeContain, Background: red})
	if err != nil {
		t.Fatal(err)
	}
	img := decodePNG(t, res.Data)
	// Source fit width 400 → inner height 100, centered → rows [0,150) are pad.
	r, g, b, a := img.At(200, 5).RGBA()
	if r>>8 != 255 || g>>8 != 0 || b>>8 != 0 || a>>8 != 255 {
		t.Errorf("top padding pixel = (%d,%d,%d,%d), want red", r>>8, g>>8, b>>8, a>>8)
	}
}

func TestContainTransparentDefault(t *testing.T) {
	data := makePNG(t, 800, 200)
	res, err := CropBytesWithOptions(data, 400, 400, Options{Mode: ModeContain})
	if err != nil {
		t.Fatal(err)
	}
	img := decodePNG(t, res.Data)
	if _, _, _, a := img.At(200, 5).RGBA(); a>>8 != 0 {
		t.Errorf("top padding alpha = %d, want 0 (transparent)", a>>8)
	}
}

func TestPadToAspectWidensAndCentersWithTransparency(t *testing.T) {
	// 2:1 source toward a 4:1 target → canvas widens to 4:1, height unchanged,
	// source centered with transparent bands left/right.
	data := makePNG(t, 800, 400)
	res, err := PadToAspectBytes(data, 1120, 280) // 4:1
	if err != nil {
		t.Fatalf("PadToAspectBytes: %v", err)
	}
	// Height stays 400; width grows to 400*4 = 1600.
	if res.Width != 1600 || res.Height != 400 {
		t.Fatalf("canvas = %dx%d, want 1600x400", res.Width, res.Height)
	}
	img := decodePNG(t, res.Data)
	// Far-left band is transparent (source is centered, band ~400px each side).
	if _, _, _, a := img.At(10, 200).RGBA(); a>>8 != 0 {
		t.Errorf("left band alpha = %d, want 0 (transparent)", a>>8)
	}
	// Center holds the opaque source.
	if _, _, _, a := img.At(800, 200).RGBA(); a>>8 != 255 {
		t.Errorf("center alpha = %d, want 255 (opaque source)", a>>8)
	}
}

func TestPadToAspectHeightensForTallTarget(t *testing.T) {
	// 2:1 source toward a 1:2 target → canvas heightens, width unchanged.
	data := makePNG(t, 800, 400)
	res, err := PadToAspectBytes(data, 280, 1120) // 1:4
	if err != nil {
		t.Fatalf("PadToAspectBytes: %v", err)
	}
	// Width stays 800; height grows to 800*4 = 3200.
	if res.Width != 800 || res.Height != 3200 {
		t.Fatalf("canvas = %dx%d, want 800x3200", res.Width, res.Height)
	}
}

func TestPadToAspectMatchingRatioReturnsSource(t *testing.T) {
	// Source already matches the target ratio → returned unchanged (no band).
	data := makePNG(t, 800, 400)                  // 2:1
	res, err := PadToAspectBytes(data, 1000, 500) // 2:1
	if err != nil {
		t.Fatalf("PadToAspectBytes: %v", err)
	}
	if res.Width != 800 || res.Height != 400 {
		t.Errorf("canvas = %dx%d, want 800x400 (unchanged)", res.Width, res.Height)
	}
}

func TestPadToAspectInvalidTarget(t *testing.T) {
	data := makePNG(t, 800, 400)
	if _, err := PadToAspectBytes(data, 0, 280); err == nil {
		t.Error("expected error for zero target width, got nil")
	}
}

func TestCompositeOutpaintKeepsMasterCenter(t *testing.T) {
	// Master: solid red 800×400 (2:1). Fill: solid blue 1600×400 (4:1 bg).
	master := makeSolidPNG(t, 800, 400, color.RGBA{220, 30, 30, 255})
	fill := makeSolidPNG(t, 1600, 400, color.RGBA{30, 30, 220, 255})

	res, err := CompositeOutpaintBytes(master, fill, 1120, 280, 0) // no feather: hard center
	if err != nil {
		t.Fatalf("CompositeOutpaintBytes: %v", err)
	}
	if res.Width != 1120 || res.Height != 280 {
		t.Fatalf("dims = %dx%d, want 1120x280", res.Width, res.Height)
	}
	img := decodePNG(t, res.Data)
	// Center pixel must be the master's red (master composited over center).
	r, g, b, _ := img.At(560, 140).RGBA()
	if r>>8 < 180 || g>>8 > 80 || b>>8 > 80 {
		t.Errorf("center pixel = R%d G%d B%d, want master red", r>>8, g>>8, b>>8)
	}
	// Far-left edge must be the fill's blue (extended margin), NOT a band.
	r, g, b, a := img.At(5, 140).RGBA()
	if a>>8 != 255 {
		t.Errorf("left edge alpha = %d, want 255 (no transparent band)", a>>8)
	}
	if b>>8 < 180 || r>>8 > 80 {
		t.Errorf("left edge = R%d G%d B%d, want fill blue", r>>8, g>>8, b>>8)
	}
}

func TestCompositeOutpaintInvalidTarget(t *testing.T) {
	master := makeSolidPNG(t, 800, 400, color.RGBA{220, 30, 30, 255})
	fill := makeSolidPNG(t, 1600, 400, color.RGBA{30, 30, 220, 255})
	if _, err := CompositeOutpaintBytes(master, fill, 0, 280, 0); err == nil {
		t.Error("expected error for zero target width, got nil")
	}
}

func TestAnchorShiftsCrop(t *testing.T) {
	// A tall source into a wide box crops vertically; top vs bottom anchors must
	// keep different regions, so the two outputs must differ.
	data := makeGradientPNG(t, 200, 800)
	top, err := CropBytesWithOptions(data, 200, 100, Options{Mode: ModeAnchor, Anchor: AnchorTop})
	if err != nil {
		t.Fatal(err)
	}
	bottom, err := CropBytesWithOptions(data, 200, 100, Options{Mode: ModeAnchor, Anchor: AnchorBottom})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(top.Data, bottom.Data) {
		t.Error("top and bottom anchor crops are identical; anchor had no effect")
	}
}

func TestCropModeInvalidParams(t *testing.T) {
	data := makePNG(t, 100, 100)
	if _, err := CropBytesWithOptions(data, 50, 50, Options{Mode: "bogus"}); err == nil {
		t.Error("expected error for unknown mode")
	}
	if _, err := CropBytesWithOptions(data, 50, 50, Options{Mode: ModeRect}); err == nil {
		t.Error("expected error for rect mode without region")
	}
	if _, err := CropBytesWithOptions(data, 50, 50, Options{Mode: ModeRect, Rect: &Rect{X: 0.5, Y: 0, W: 0.8, H: 0.5}}); err == nil {
		t.Error("expected error for out-of-range rect")
	}
	if _, err := CropBytesWithOptions(data, 50, 50, Options{Mode: ModeAnchor, Anchor: "nowhere"}); err == nil {
		t.Error("expected error for invalid anchor")
	}
}

// makeGradientPNG builds a vertical gradient so anchored crops differ by region.
// makeSolidPNG builds a w×h PNG filled with a single color.
func makeSolidPNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeGradientPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		v := uint8((y * 255) / h)
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
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

	results, err := svc.CropToSizes("s", "src", []string{"test.square", "test.wide"}, true, Options{})
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
	if _, err := svc.CropToSizes("s", "missing", []string{"test.square"}, true, Options{}); err == nil {
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
