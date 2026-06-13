package imageopt

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// makePNG builds a small PNG with deliberately compressible content.
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Large flat regions compress well, exposing optimization gains.
			c := color.RGBA{uint8((x / 8) * 8), uint8((y / 8) * 8), 128, 255}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	// Encode at no/default compression so BestCompression can improve on it.
	enc := png.Encoder{CompressionLevel: png.NoCompression}
	if err := enc.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodePixels(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return img
}

func TestOptimizePNGIsLosslessAndSmaller(t *testing.T) {
	orig := makePNG(t, 128, 128)
	out := OptimizePNG(orig)

	if len(out) >= len(orig) {
		t.Errorf("expected smaller output: orig=%d out=%d", len(orig), len(out))
	}
	// Pixel data must be identical (true lossless).
	a := decodePixels(t, orig)
	b := decodePixels(t, out)
	if a.Bounds() != b.Bounds() {
		t.Fatalf("bounds differ: %v vs %v", a.Bounds(), b.Bounds())
	}
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			r1, g1, b1, al1 := a.At(x, y).RGBA()
			r2, g2, b2, al2 := b.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 || al1 != al2 {
				t.Fatalf("pixel mismatch at (%d,%d)", x, y)
			}
		}
	}
}

func TestOptimizeNonPNGPassthrough(t *testing.T) {
	jpegish := []byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3}
	out := Optimize(jpegish, true)
	if !bytes.Equal(out, jpegish) {
		t.Error("non-PNG must pass through unchanged")
	}
}

func TestOptimizeDisabledBypass(t *testing.T) {
	orig := makePNG(t, 64, 64)
	out := Optimize(orig, false)
	if !bytes.Equal(out, orig) {
		t.Error("lossless=false must return original bytes")
	}
}

func TestOptimizeCorruptPNGFallsBack(t *testing.T) {
	// Valid signature, garbage body: decode fails, original returned.
	bad := append(append([]byte{}, pngMagic...), []byte("not really a png")...)
	out := OptimizePNG(bad)
	if !bytes.Equal(out, bad) {
		t.Error("undecodable PNG must fall back to original")
	}
}
