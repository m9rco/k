package vision

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// solidPNG returns a uniform-color PNG of the given size.
func solidPNG(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	return encodePNG(img)
}

// checkerPNG returns a high-contrast 1px checkerboard (maximum sharpness).
func checkerPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			if (x+y)%2 == 0 {
				img.Set(x, y, color.RGBA{0, 0, 0, 255})
			} else {
				img.Set(x, y, color.RGBA{255, 255, 255, 255})
			}
		}
	}
	return encodePNG(img)
}

// borderPNG returns a PNG with a solid white left band (bandW columns) and
// a checkerboard pattern for the rest.
func borderPNG(w, h, bandW int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			if x < bandW {
				img.Set(x, y, color.RGBA{255, 255, 255, 255})
			} else if (x+y)%2 == 0 {
				img.Set(x, y, color.RGBA{0, 0, 0, 255})
			} else {
				img.Set(x, y, color.RGBA{255, 255, 255, 255})
			}
		}
	}
	return encodePNG(img)
}

func TestPixelCheckerNilPassesAll(t *testing.T) {
	var pc *PixelChecker
	v, err := pc.Check(solidPNG(100, 100, color.RGBA{0, 0, 0, 255}), "image/png")
	if err != nil || !v.Pass {
		t.Fatalf("nil checker should always pass; pass=%v err=%v", v.Pass, err)
	}
}

func TestPixelBlurDetectsSolidColor(t *testing.T) {
	pc := NewPixelChecker(80, 0)
	// Solid color → Laplacian variance = 0 < 80 → blurry.
	v, err := pc.Check(solidPNG(100, 100, color.RGBA{128, 128, 128, 255}), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if v.Pass {
		t.Error("solid-color image should fail blur check")
	}
}

func TestPixelBlurPassesCheckerboard(t *testing.T) {
	pc := NewPixelChecker(80, 0)
	// High-contrast checkerboard → high Laplacian variance → sharp.
	v, err := pc.Check(checkerPNG(100, 100), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Pass {
		t.Errorf("checkerboard should pass blur check; reasons=%v", v.Reasons)
	}
}

func TestPixelBorderDetectsUniformBand(t *testing.T) {
	pc := NewPixelChecker(0, 0.15)
	// 20% left border (20 of 100 cols) > 15% threshold → border detected.
	v, err := pc.Check(borderPNG(100, 100, 20), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if v.Pass {
		t.Errorf("image with 20%% uniform border should fail; reasons=%v", v.Reasons)
	}
}

func TestPixelBorderPassesNarrowBand(t *testing.T) {
	pc := NewPixelChecker(0, 0.15)
	// 10% left border < 15% threshold → passes.
	v, err := pc.Check(borderPNG(100, 100, 10), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Pass {
		t.Errorf("image with 10%% border should pass; reasons=%v", v.Reasons)
	}
}

func TestNewPixelCheckerBothZeroReturnsNil(t *testing.T) {
	if NewPixelChecker(0, 0) != nil {
		t.Error("both-zero NewPixelChecker should return nil")
	}
}
