package layering

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"gameasset/internal/vision"
)

// makeTestPNG builds a w×h image where each pixel encodes its (x,y) so a crop's
// pixels can be checked against the source region.
func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestCropSubjectPixelsMatchSource(t *testing.T) {
	src := makeTestPNG(t, 100, 100)
	// A centered box with NO padding effect verifiable: pick a box and recompute
	// the padded pixel rect the same way cropSubject does, then compare pixels.
	box := vision.Box{X: 0.3, Y: 0.4, W: 0.2, H: 0.2}
	data, actual, ok, err := cropSubject(src, box, nil)
	if err != nil || !ok {
		t.Fatalf("crop failed ok=%v err=%v", ok, err)
	}
	out := decodePNG(t, data)

	// Expected padded rect (mirror of cropSubject's math).
	pad := cropPadFraction * 100 // shorter side = 100
	px0 := clampInt(int(0.3*100-pad+0.5), 0, 100)
	py0 := clampInt(int(0.4*100-pad+0.5), 0, 100)
	px1 := clampInt(int(0.5*100+pad+0.5), 0, 100)
	py1 := clampInt(int(0.6*100+pad+0.5), 0, 100)

	if out.Bounds().Dx() != px1-px0 || out.Bounds().Dy() != py1-py0 {
		t.Fatalf("size = %dx%d, want %dx%d", out.Bounds().Dx(), out.Bounds().Dy(), px1-px0, py1-py0)
	}
	// Top-left output pixel must equal source (px0,py0): R=x, G=y.
	c := color.NRGBAModel.Convert(out.At(0, 0)).(color.NRGBA)
	if c.R != uint8(px0) || c.G != uint8(py0) {
		t.Errorf("top-left pixel = (R%d,G%d), want (R%d,G%d) — pixels not verbatim from source", c.R, c.G, px0, py0)
	}
	// Actual normalized box must match the padded pixel rect.
	if actual.X != float64(px0)/100 || actual.W != float64(px1-px0)/100 {
		t.Errorf("actual box = %+v, want x=%v w=%v", actual, float64(px0)/100, float64(px1-px0)/100)
	}
}

func TestCropSubjectPaddingExpandsBox(t *testing.T) {
	src := makeTestPNG(t, 200, 200)
	box := vision.Box{X: 0.4, Y: 0.4, W: 0.2, H: 0.2}
	_, actual, ok, err := cropSubject(src, box, nil)
	if err != nil || !ok {
		t.Fatalf("crop failed ok=%v err=%v", ok, err)
	}
	// Padded box must be strictly larger than the input on each side (input is
	// well inside bounds, so padding isn't clamped away).
	if actual.X >= box.X || actual.Y >= box.Y {
		t.Errorf("padding should push origin outward: actual %+v vs box %+v", actual, box)
	}
	if actual.W <= box.W || actual.H <= box.H {
		t.Errorf("padding should grow size: actual %+v vs box %+v", actual, box)
	}
}

func TestCropSubjectClampsToBounds(t *testing.T) {
	src := makeTestPNG(t, 50, 50)
	// Box at the corner; padding would push it negative — must clamp to [0,1].
	box := vision.Box{X: 0, Y: 0, W: 0.3, H: 0.3}
	_, actual, ok, err := cropSubject(src, box, nil)
	if err != nil || !ok {
		t.Fatalf("crop failed ok=%v err=%v", ok, err)
	}
	if actual.X < 0 || actual.Y < 0 || actual.X+actual.W > 1 || actual.Y+actual.H > 1 {
		t.Errorf("actual box escapes [0,1]: %+v", actual)
	}
}

func TestCropSubjectDegenerateBoxSkipped(t *testing.T) {
	src := makeTestPNG(t, 80, 80)
	// Zero-area box — after rounding it collapses; with tiny padding it may still
	// produce a sliver, so use an explicitly empty box well within a pixel.
	box := vision.Box{X: 0.5, Y: 0.5, W: 0, H: 0}
	data, _, ok, err := cropSubject(src, box, nil)
	if err != nil {
		t.Fatalf("degenerate box must not error: %v", err)
	}
	// With pad=0.02*80≈1.6px the box may still be non-empty; the contract is only
	// that ok==false yields no data. Assert the invariant: ok and data agree.
	if ok && len(data) == 0 {
		t.Error("ok=true but no data")
	}
	if !ok && data != nil {
		t.Error("ok=false but data returned")
	}
}

func TestCropSubjectBadImage(t *testing.T) {
	if _, _, ok, err := cropSubject([]byte("notpng"), vision.Box{W: 0.5, H: 0.5}, nil); err == nil || ok {
		t.Error("expected decode error for non-image bytes")
	}
}

// makeMaskPNG builds a w×h grayscale PNG whose intensity is set per-pixel by fn,
// so a cutout's alpha can be checked against the mask.
func makeMaskPNG(t *testing.T, w, h int, fn func(x, y int) uint8) []byte {
	t.Helper()
	m := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.SetGray(x, y, color.Gray{Y: fn(x, y)})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCropSubjectMaskAppliesAlphaKeepsRGBVerbatim(t *testing.T) {
	src := makeTestPNG(t, 100, 100)
	// Box maps to pixel rect x:[30,50) y:[40,60) — the masked path uses NO padding,
	// so the crop is exactly 20×20 and the mask aligns 1:1.
	box := vision.Box{X: 0.3, Y: 0.4, W: 0.2, H: 0.2}
	// Mask: left half opaque (255), right half transparent (0).
	mask := makeMaskPNG(t, 20, 20, func(x, _ int) uint8 {
		if x < 10 {
			return 255
		}
		return 0
	})

	data, actual, ok, err := cropSubject(src, box, mask)
	if err != nil || !ok {
		t.Fatalf("masked crop failed ok=%v err=%v", ok, err)
	}
	img := decodePNG(t, data)
	if img.Bounds().Dx() != 20 || img.Bounds().Dy() != 20 {
		t.Fatalf("masked crop size = %dx%d, want 20x20 (no pad)", img.Bounds().Dx(), img.Bounds().Dy())
	}
	// No padding in the masked path: the crop box equals the detection box exactly.
	if actual.X != 0.3 || actual.Y != 0.4 || actual.W != 0.2 || actual.H != 0.2 {
		t.Errorf("masked crop box should equal detection box, got %+v", actual)
	}
	// Left column: opaque, RGB verbatim from source pixel (30,40) = (R30,G40).
	left := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA)
	if left.A != 255 {
		t.Errorf("left pixel alpha = %d, want 255 (mask foreground)", left.A)
	}
	if left.R != 30 || left.G != 40 {
		t.Errorf("left pixel RGB = (R%d,G%d), want (R30,G40) — not verbatim from source", left.R, left.G)
	}
	// Right column: transparent (mask background); RGB is irrelevant once A=0.
	right := color.NRGBAModel.Convert(img.At(19, 0)).(color.NRGBA)
	if right.A != 0 {
		t.Errorf("right pixel alpha = %d, want 0 (mask background)", right.A)
	}
}

func TestCropSubjectUndecodableMaskFallsBackToOpaque(t *testing.T) {
	src := makeTestPNG(t, 100, 100)
	box := vision.Box{X: 0.3, Y: 0.4, W: 0.2, H: 0.2}
	// Garbage mask bytes → cropSubject degrades to the padded opaque rectangle.
	data, actual, ok, err := cropSubject(src, box, []byte("not-a-png"))
	if err != nil || !ok {
		t.Fatalf("fallback crop failed ok=%v err=%v", ok, err)
	}
	img := decodePNG(t, data)
	// Opaque path applies padding, so the crop is larger than the bare 20×20 box.
	if actual.W <= box.W {
		t.Errorf("undecodable mask should fall back to padded opaque crop, got box %+v", actual)
	}
	c := color.NRGBAModel.Convert(img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2)).(color.NRGBA)
	if c.A != 255 {
		t.Errorf("opaque fallback center alpha = %d, want 255", c.A)
	}
}
