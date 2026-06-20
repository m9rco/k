package crop

import (
	"image"
	"image/color"
	"testing"
)

// makeEdgeRGBA builds a w×h image split vertically into a dark left half and a
// light right half — a single hard edge whose contrast we can measure before and
// after sharpening.
func makeEdgeRGBA(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(80)
			if x >= w/2 {
				v = 180
			}
			img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	return img
}

// edgeContrast returns the max absolute jump in the red channel across the
// vertical center line of row y — a proxy for edge crispness.
func edgeContrast(img *image.RGBA, y int) int {
	b := img.Bounds()
	w := b.Dx()
	maxJump := 0
	for x := 1; x < w; x++ {
		i0 := img.PixOffset(b.Min.X+x-1, b.Min.Y+y)
		i1 := img.PixOffset(b.Min.X+x, b.Min.Y+y)
		j := int(img.Pix[i1]) - int(img.Pix[i0])
		if j < 0 {
			j = -j
		}
		if j > maxJump {
			maxJump = j
		}
	}
	return maxJump
}

// TestSharpenIncreasesEdgeContrast verifies the unsharp mask raises edge contrast
// (overshoot at the boundary) without altering dimensions, simulating the
// post-downscale re-crisping path.
func TestSharpenIncreasesEdgeContrast(t *testing.T) {
	const w, h = 40, 10
	img := makeEdgeRGBA(w, h)
	before := edgeContrast(img, h/2)

	// Force a meaningful sharpen amount (as if downscaled ~2×).
	unsharpMask(img, 0.5, 1.0)

	after := edgeContrast(img, h/2)
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("dimensions changed: got %dx%d, want %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), w, h)
	}
	if after <= before {
		t.Errorf("edge contrast did not increase: before=%d after=%d", before, after)
	}
}

// TestSharpenForDownscaleNoOpOnUpscale verifies sharpening is skipped when the
// "downscale" ratio is below threshold (an upscale or near-1:1 copy), so we don't
// amplify interpolation noise.
func TestSharpenForDownscaleNoOpOnUpscale(t *testing.T) {
	const w, h = 40, 10
	img := makeEdgeRGBA(w, h)
	snapshot := make([]uint8, len(img.Pix))
	copy(snapshot, img.Pix)

	// dst larger than src → ratio < 1 → must be a no-op.
	sharpenForDownscale(img, 20, 5, 40, 10)

	for i := range img.Pix {
		if img.Pix[i] != snapshot[i] {
			t.Fatalf("sharpenForDownscale mutated pixels on an upscale at byte %d", i)
		}
	}
}

// TestSharpenPreservesFlatColor verifies a uniform region is untouched (no halo,
// no banding) — the unsharp mask only acts where there is local contrast.
func TestSharpenPreservesFlatColor(t *testing.T) {
	const w, h = 20, 20
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{120, 120, 120, 255})
		}
	}
	unsharpMask(img, 0.8, 1.0)
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 120 || img.Pix[i+1] != 120 || img.Pix[i+2] != 120 {
			t.Fatalf("flat color changed at byte %d: got %d,%d,%d", i, img.Pix[i], img.Pix[i+1], img.Pix[i+2])
		}
	}
}

// TestSharpenPreservesAlpha verifies the alpha channel is never modified.
func TestSharpenPreservesAlpha(t *testing.T) {
	const w, h = 40, 10
	img := makeEdgeRGBA(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			img.Pix[i+3] = uint8((x * 6) % 256) // varying alpha
		}
	}
	want := make([]uint8, h*w)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			want[y*w+x] = img.Pix[img.PixOffset(x, y)+3]
		}
	}
	unsharpMask(img, 0.8, 1.0)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if got := img.Pix[img.PixOffset(x, y)+3]; got != want[y*w+x] {
				t.Fatalf("alpha changed at (%d,%d): got %d want %d", x, y, got, want[y*w+x])
			}
		}
	}
}
