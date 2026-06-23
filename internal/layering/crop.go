package layering

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"

	_ "image/jpeg" // decode JPEG sources
	_ "image/png"  // decode PNG sources

	xdraw "golang.org/x/image/draw"

	"gameasset/internal/vision"
)

// cropPadFraction expands every detected box outward by this fraction of the
// source image's SHORTER side before cropping, so a slightly-tight detection box
// doesn't shave the subject's edge. It is a deterministic pixel margin, not an AI
// guess. Clamped to the image bounds. Only used for the opaque-rectangle fallback
// (a masked cutout crops the box exactly — the mask defines the edges).
const cropPadFraction = 0.02

// cropSubject cuts the subject described by box out of the source image and
// returns its PNG bytes alongside the actual normalized box it occupies. Pixels
// are copied VERBATIM from the source — no scaling, no repaint — so the layer
// drops back onto a source-sized canvas in perfect register with the original.
//
// When mask is non-empty (a segmentation probability map sized to box), it is
// applied as the alpha channel so only the subject's pixels stay opaque and the
// surrounding background goes transparent — a true cutout (透明底) whose RGB still
// comes 100% from the original. When mask is nil/undecodable, cropSubject falls
// back to an opaque rectangular crop (the detection box plus a small pad margin).
// Returns ok=false for a degenerate box (zero area after clamping) so the caller
// skips it.
func cropSubject(src []byte, box vision.Box, mask []byte) (data []byte, actual vision.Box, ok bool, err error) {
	img, _, derr := image.Decode(bytes.NewReader(src))
	if derr != nil {
		return nil, vision.Box{}, false, fmt.Errorf("decode source: %w", derr)
	}
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if srcW <= 0 || srcH <= 0 {
		return nil, vision.Box{}, false, fmt.Errorf("source has no pixels")
	}

	// Decode the segmentation mask up front; if it fails to decode we degrade to an
	// opaque rectangular crop (treat it as no mask).
	var maskImg image.Image
	if len(mask) > 0 {
		if m, _, merr := image.Decode(bytes.NewReader(mask)); merr == nil {
			maskImg = m
		}
	}

	// A masked cutout crops the detection box EXACTLY (the mask defines the edges).
	// An opaque rectangle expands by the pad margin so a slightly-tight box doesn't
	// shave the subject.
	pad := 0.0
	if maskImg == nil {
		pad = cropPadFraction * float64(min(srcW, srcH))
	}
	x0 := float64(box.X)*float64(srcW) - pad
	y0 := float64(box.Y)*float64(srcH) - pad
	x1 := float64(box.X+box.W)*float64(srcW) + pad
	y1 := float64(box.Y+box.H)*float64(srcH) + pad

	px0 := clampInt(int(x0+0.5), 0, srcW)
	py0 := clampInt(int(y0+0.5), 0, srcH)
	px1 := clampInt(int(x1+0.5), 0, srcW)
	py1 := clampInt(int(y1+0.5), 0, srcH)
	if px1 <= px0 || py1 <= py0 {
		return nil, vision.Box{}, false, nil // degenerate box → skip
	}

	// Copy the rect verbatim into a same-size NRGBA layer (preserves any alpha).
	rect := image.Rect(0, 0, px1-px0, py1-py0)
	out := image.NewNRGBA(rect)
	draw.Draw(out, rect, img, image.Pt(b.Min.X+px0, b.Min.Y+py0), draw.Src)

	// With a mask, overwrite the alpha channel with the (resized) mask intensity so
	// only the subject stays opaque. RGB is untouched — no repaint — so the cutout
	// drops back in perfect register with the original.
	if maskImg != nil {
		applyMaskAlpha(out, maskImg)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, vision.Box{}, false, fmt.Errorf("encode layer: %w", err)
	}

	actual = vision.Box{
		X: float64(px0) / float64(srcW),
		Y: float64(py0) / float64(srcH),
		W: float64(px1-px0) / float64(srcW),
		H: float64(py1-py0) / float64(srcH),
	}
	return buf.Bytes(), actual, true, nil
}

// applyMaskAlpha resizes maskImg to out's bounds and multiplies each pixel's alpha
// by the mask intensity (0 = transparent background, 255 = opaque subject),
// leaving RGB untouched. The mask is a grayscale probability map; reading its
// luminance keeps soft, anti-aliased subject edges.
func applyMaskAlpha(out *image.NRGBA, maskImg image.Image) {
	bnd := out.Bounds()
	scaled := image.NewGray(bnd)
	xdraw.ApproxBiLinear.Scale(scaled, bnd, maskImg, maskImg.Bounds(), xdraw.Src, nil)
	for y := bnd.Min.Y; y < bnd.Max.Y; y++ {
		for x := bnd.Min.X; x < bnd.Max.X; x++ {
			mv := scaled.GrayAt(x, y).Y
			i := out.PixOffset(x, y)
			out.Pix[i+3] = uint8(uint16(out.Pix[i+3]) * uint16(mv) / 255)
		}
	}
}

// clampInt constrains v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
