package crop

import (
	"image"
	"math"
)

// sharpenForDownscale applies an unsharp mask to a freshly-resampled image to
// restore the edge contrast that downscaling softens. CatmullRom (the sharpest
// filter in x/image/draw) still produces a slightly "soft" result when shrinking
// a high-res master into a small platform size; this re-crisps the edges WITHOUT
// changing pixel dimensions, so the delivered size stays exactly the catalog
// nominal while looking sharp rather than blurry.
//
// It is a no-op unless the operation was a meaningful downscale (ratio ≥ 1.15):
// sharpening an upscale or a near-1:1 copy only amplifies interpolation noise.
// The amount scales with how aggressively the image was shrunk — a heavier
// reduction discards more high-frequency detail and needs more correction —
// clamped so even an extreme reduction never produces visible halos.
func sharpenForDownscale(img *image.RGBA, srcW, srcH, dstW, dstH int) {
	if dstW <= 0 || dstH <= 0 || srcW <= 0 || srcH <= 0 {
		return
	}
	ratio := math.Min(float64(srcW)/float64(dstW), float64(srcH)/float64(dstH))
	if ratio < 1.15 {
		return // not a meaningful downscale → leave the pixels untouched
	}
	// Map downscale ratio → sharpen amount: ~0.35 at the 1.15 threshold, growing
	// with log2(ratio) toward a 0.9 cap so a 5× reduction is corrected hard but
	// never haloes.
	amount := 0.35 + 0.18*math.Log2(ratio)
	if amount > 0.9 {
		amount = 0.9
	}
	unsharpMask(img, amount, 1.0)
}

// unsharpMask sharpens img in place: result = orig + amount·(orig − blur), where
// blur is a separable Gaussian of the given sigma. Only the RGB channels are
// touched; alpha is preserved so transparent padding and feathered edges stay
// intact. amount is the edge-contrast gain (0 = no-op); sigma sets the radius of
// detail affected (~1px for crisp re-sharpening after a downscale).
func unsharpMask(img *image.RGBA, amount, sigma float64) {
	if amount <= 0 || sigma <= 0 {
		return
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return
	}
	kernel := gaussianKernel(sigma)
	radius := len(kernel) / 2
	n := w * h

	// Snapshot the original RGB into float buffers (row-major, origin-relative).
	origR := make([]float64, n)
	origG := make([]float64, n)
	origB := make([]float64, n)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(b.Min.X+x, b.Min.Y+y)
			p := img.Pix[i : i+4 : i+4]
			j := y*w + x
			origR[j] = float64(p[0])
			origG[j] = float64(p[1])
			origB[j] = float64(p[2])
		}
	}

	// Horizontal Gaussian pass (clamp at edges) into temp buffers.
	tmpR := make([]float64, n)
	tmpG := make([]float64, n)
	tmpB := make([]float64, n)
	for y := 0; y < h; y++ {
		row := y * w
		for x := 0; x < w; x++ {
			var sr, sg, sb float64
			for t := -radius; t <= radius; t++ {
				xx := x + t
				if xx < 0 {
					xx = 0
				} else if xx >= w {
					xx = w - 1
				}
				kv := kernel[t+radius]
				j := row + xx
				sr += origR[j] * kv
				sg += origG[j] * kv
				sb += origB[j] * kv
			}
			j := row + x
			tmpR[j], tmpG[j], tmpB[j] = sr, sg, sb
		}
	}

	// Vertical Gaussian pass over the horizontal result, then apply the mask.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var sr, sg, sb float64
			for t := -radius; t <= radius; t++ {
				yy := y + t
				if yy < 0 {
					yy = 0
				} else if yy >= h {
					yy = h - 1
				}
				kv := kernel[t+radius]
				j := yy*w + x
				sr += tmpR[j] * kv
				sg += tmpG[j] * kv
				sb += tmpB[j] * kv
			}
			j := y*w + x
			i := img.PixOffset(b.Min.X+x, b.Min.Y+y)
			p := img.Pix[i : i+4 : i+4]
			p[0] = clampU8(origR[j] + amount*(origR[j]-sr))
			p[1] = clampU8(origG[j] + amount*(origG[j]-sg))
			p[2] = clampU8(origB[j] + amount*(origB[j]-sb))
			// p[3] (alpha) left unchanged.
		}
	}
}

// gaussianKernel returns a normalized 1-D Gaussian kernel for the given sigma,
// with radius = ceil(3σ) (covers >99% of the distribution).
func gaussianKernel(sigma float64) []float64 {
	radius := int(math.Ceil(sigma * 3))
	if radius < 1 {
		radius = 1
	}
	k := make([]float64, 2*radius+1)
	var sum float64
	twoSigma2 := 2 * sigma * sigma
	for i := -radius; i <= radius; i++ {
		v := math.Exp(-float64(i*i) / twoSigma2)
		k[i+radius] = v
		sum += v
	}
	for i := range k {
		k[i] /= sum
	}
	return k
}

// clampU8 rounds v and clamps it to the uint8 range.
func clampU8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}
