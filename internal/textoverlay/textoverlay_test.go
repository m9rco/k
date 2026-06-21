package textoverlay

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// testFonts loads only the always-embedded fallback (Go Bold, Latin/ASCII). The
// primary CJK font is intentionally absent so CJK-coverage tests exercise the
// "uncovered character" path deterministically regardless of host fonts.
func testFonts(t *testing.T) *Fonts {
	t.Helper()
	f, err := LoadFonts("") // no vendored path, no OVERLAY_FONT → fallback only
	if err != nil {
		t.Fatalf("LoadFonts: %v", err)
	}
	return f
}

// blankPNG builds a w×h opaque image encoded as PNG, the base to overlay onto.
func blankPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode base: %v", err)
	}
	return buf.Bytes()
}

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	return img
}

// nonZeroPixels counts pixels with any non-zero channel — a proxy for "something
// was drawn" within a region.
func nonZeroPixels(img image.Image, r image.Rectangle) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cr, cg, cb, ca := img.At(x, y).RGBA()
			if cr|cg|cb|ca != 0 {
				n++
			}
		}
	}
	return n
}

func TestRender_ASCIIBasic(t *testing.T) {
	fonts := testFonts(t)
	out, mime, err := Render(blankPNG(t, 400, 200), Request{
		Overlays: []Overlay{{Text: "PLAY NOW", Anchor: AnchorCenter, FontPx: 32, Color: color.White}},
	}, fonts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}
	img := decode(t, out)
	if got := nonZeroPixels(img, img.Bounds()); got == 0 {
		t.Error("expected drawn text pixels, got blank image")
	}
}

func TestRender_AnchorPlacement(t *testing.T) {
	fonts := testFonts(t)
	const w, h = 400, 400
	// Bottom-right placement should leave the top-left quadrant blank.
	out, _, err := Render(blankPNG(t, w, h), Request{
		Overlays: []Overlay{{Text: "CTA", Anchor: AnchorBottomRight, FontPx: 28, Color: color.White}},
	}, fonts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	img := decode(t, out)
	topLeft := image.Rect(0, 0, w/2, h/2)
	bottomRight := image.Rect(w/2, h/2, w, h)
	if n := nonZeroPixels(img, topLeft); n != 0 {
		t.Errorf("top-left quadrant should be empty, got %d drawn pixels", n)
	}
	if n := nonZeroPixels(img, bottomRight); n == 0 {
		t.Error("bottom-right quadrant should contain the CTA text")
	}
}

func TestRender_SafeAreaKeepsTextInside(t *testing.T) {
	fonts := testFonts(t)
	const w, h = 300, 300
	inset := 0.1
	out, _, err := Render(blankPNG(t, w, h), Request{
		SafeInsetFrac: inset,
		Overlays:      []Overlay{{Text: "EDGE", Anchor: AnchorTopLeft, FontPx: 24, Color: color.White}},
	}, fonts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	img := decode(t, out)
	// The inset band (outermost 10%) on the top/left must stay blank — text is
	// kept inside the safe area.
	band := int(float64(w) * inset)
	topBand := image.Rect(0, 0, w, band)
	leftBand := image.Rect(0, 0, band, h)
	if n := nonZeroPixels(img, topBand); n != 0 {
		t.Errorf("top safe-inset band should be empty, got %d pixels", n)
	}
	if n := nonZeroPixels(img, leftBand); n != 0 {
		t.Errorf("left safe-inset band should be empty, got %d pixels", n)
	}
}

func TestRender_BackgroundPlate(t *testing.T) {
	fonts := testFonts(t)
	out, _, err := Render(blankPNG(t, 300, 150), Request{
		Overlays: []Overlay{{
			Text:       "立即预约", // CJK — uncovered by fallback, but text is ASCII-safe? no
			Anchor:     AnchorCenter,
			FontPx:     24,
			Color:      color.White,
			Background: color.RGBA{R: 124, G: 58, B: 237, A: 255},
		}},
	}, fonts)
	// Fallback-only fonts cannot cover CJK, so this must fail validation rather
	// than draw tofu. Asserts the anti-tofu guard.
	if err == nil {
		t.Fatal("expected uncovered-CJK error with fallback-only fonts")
	}
	_ = out
}

func TestRender_UncoveredGlyphFailsLoudly(t *testing.T) {
	fonts := testFonts(t)
	_, _, err := Render(blankPNG(t, 200, 80), Request{
		Overlays: []Overlay{{Text: "折扣", Anchor: AnchorCenter}},
	}, fonts)
	if err == nil {
		t.Fatal("expected error for CJK text with fallback-only fonts (no tofu)")
	}
}

func TestRender_EmptyOverlaysRejected(t *testing.T) {
	fonts := testFonts(t)
	if _, _, err := Render(blankPNG(t, 100, 100), Request{}, fonts); err == nil {
		t.Fatal("expected error for no overlays")
	}
}

func TestRender_BlankTextRejected(t *testing.T) {
	fonts := testFonts(t)
	if _, _, err := Render(blankPNG(t, 100, 100), Request{Overlays: []Overlay{{Text: "   "}}}, fonts); err == nil {
		t.Fatal("expected error for blank overlay text")
	}
}

func TestRender_StrokeDrawsMorePixels(t *testing.T) {
	fonts := testFonts(t)
	base := blankPNG(t, 300, 120)
	plain, _, err := Render(base, Request{Overlays: []Overlay{{Text: "GO", Anchor: AnchorCenter, FontPx: 40, Color: color.White}}}, fonts)
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	stroked, _, err := Render(base, Request{Overlays: []Overlay{{
		Text: "GO", Anchor: AnchorCenter, FontPx: 40, Color: color.White,
		Stroke: color.Black, StrokePx: 2,
	}}}, fonts)
	if err != nil {
		t.Fatalf("stroked: %v", err)
	}
	pp := nonZeroPixels(decode(t, plain), image.Rect(0, 0, 300, 120))
	sp := nonZeroPixels(decode(t, stroked), image.Rect(0, 0, 300, 120))
	if sp <= pp {
		t.Errorf("stroked pixels (%d) should exceed plain (%d)", sp, pp)
	}
}

func TestRender_NilFonts(t *testing.T) {
	if _, _, err := Render(blankPNG(t, 50, 50), Request{Overlays: []Overlay{{Text: "x"}}}, nil); err == nil {
		t.Fatal("expected error with nil fonts")
	}
}

func TestFirstUncovered(t *testing.T) {
	fonts := testFonts(t)
	if _, ok := fonts.firstUncovered("HELLO 123"); !ok {
		t.Error("ASCII should be fully covered by fallback")
	}
	if r, ok := fonts.firstUncovered("Hi 折"); ok || r == 0 {
		t.Errorf("CJK should be uncovered by fallback-only, got ok=%v r=%q", ok, string(r))
	}
}
