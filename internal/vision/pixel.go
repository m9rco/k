package vision

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"math"
	"strings"

	_ "golang.org/x/image/webp"
)

// PixelVerdict is the result of a pixel-level quality check.
type PixelVerdict struct {
	Pass    bool
	Reasons []string
	Hints   string
}

// PixelChecker performs fast algorithmic quality checks (blur + border fill)
// before the AI judge. Pure Go, zero external I/O, typically <10ms.
type PixelChecker struct {
	blurThreshold  float64 // Laplacian-variance lower bound; 0 = disabled
	borderMaxRatio float64 // max uniform-edge fraction; 0 = disabled
}

// NewPixelChecker returns a PixelChecker, or nil when both thresholds are zero
// (which disables pixel checking transparently).
func NewPixelChecker(blurThreshold int, borderMaxRatio float64) *PixelChecker {
	if blurThreshold <= 0 && borderMaxRatio <= 0 {
		return nil
	}
	return &PixelChecker{blurThreshold: float64(blurThreshold), borderMaxRatio: borderMaxRatio}
}

// Check returns a failing verdict if the image is detectably blurry or has a
// large uniform-color border band. Any decode error degrades to pass.
func (c *PixelChecker) Check(imgBytes []byte, mime string) (PixelVerdict, error) {
	if c == nil {
		return PixelVerdict{Pass: true}, nil
	}
	img, err := decodeImg(imgBytes, mime)
	if err != nil {
		return PixelVerdict{Pass: true}, fmt.Errorf("pixel: decode: %w", err)
	}

	var reasons, hints []string
	if c.blurThreshold > 0 && lapVariance(img) < c.blurThreshold {
		reasons = append(reasons, "图像模糊，清晰度不足")
		hints = append(hints, "提升清晰度，强化锐利边缘")
	}
	if c.borderMaxRatio > 0 && hasUniformBorder(img, c.borderMaxRatio) {
		reasons = append(reasons, "存在纯色留白条带")
		hints = append(hints, "画面应完整填充，无纯色边框留白")
	}
	if len(reasons) > 0 {
		return PixelVerdict{Pass: false, Reasons: reasons, Hints: strings.Join(hints, "；")}, nil
	}
	return PixelVerdict{Pass: true}, nil
}

// lapVariance computes the variance of the 3×3 Laplacian response across the
// image. High = sharp; low = blurry.
func lapVariance(img image.Image) float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 3 || h < 3 {
		return 0
	}
	// Grayscale buffer (values in 0–65535 from RGBA()).
	gray := make([]float64, w*h)
	for y := range h {
		for x := range w {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			gray[y*w+x] = 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)
		}
	}
	var sum, sumSq float64
	n := 0
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			v := -4*gray[y*w+x] +
				gray[(y-1)*w+x] + gray[(y+1)*w+x] +
				gray[y*w+x-1] + gray[y*w+x+1]
			sum += v
			sumSq += v * v
			n++
		}
	}
	if n == 0 {
		return 0
	}
	mean := sum / float64(n)
	return sumSq/float64(n) - mean*mean
}

// colorVarThresh is the sum-of-per-channel variance below which a row/column
// is considered uniform (values on 0–65535 scale; ≈ ±3 units in 0-255 scale).
const colorVarThresh = 2_000_000.0

// hasUniformBorder returns true when any of the four edges has a band of
// uniform-color rows/columns wider than maxRatio of the image dimension.
func hasUniformBorder(img image.Image, maxRatio float64) bool {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 8 || h < 8 {
		return false
	}
	scanFrac := math.Max(0.08, maxRatio)
	scanH := int(math.Max(1, float64(h)*scanFrac))
	scanW := int(math.Max(1, float64(w)*scanFrac))
	maxH := int(math.Ceil(float64(h) * maxRatio))
	maxW := int(math.Ceil(float64(w) * maxRatio))

	if countUniformRows(img, b, 0, scanH, w) >= maxH {
		return true
	}
	if countUniformRows(img, b, h-scanH, h, w) >= maxH {
		return true
	}
	if countUniformCols(img, b, 0, scanW, h) >= maxW {
		return true
	}
	if countUniformCols(img, b, w-scanW, w, h) >= maxW {
		return true
	}
	return false
}

func countUniformRows(img image.Image, b image.Rectangle, y0, y1, w int) int {
	n := 0
	for y := y0; y < y1; y++ {
		if rowUniform(img, b, y, w) {
			n++
		}
	}
	return n
}

func rowUniform(img image.Image, b image.Rectangle, y, w int) bool {
	var sR, sG, sB, s2R, s2G, s2B float64
	for x := range w {
		r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
		fr, fg, fb := float64(r), float64(g), float64(bl)
		sR += fr
		s2R += fr * fr
		sG += fg
		s2G += fg * fg
		sB += fb
		s2B += fb * fb
	}
	n := float64(w)
	vR := s2R/n - (sR/n)*(sR/n)
	vG := s2G/n - (sG/n)*(sG/n)
	vB := s2B/n - (sB/n)*(sB/n)
	return vR+vG+vB < colorVarThresh
}

func countUniformCols(img image.Image, b image.Rectangle, x0, x1, h int) int {
	n := 0
	for x := x0; x < x1; x++ {
		if colUniform(img, b, x, h) {
			n++
		}
	}
	return n
}

func colUniform(img image.Image, b image.Rectangle, x, h int) bool {
	var sR, sG, sB, s2R, s2G, s2B float64
	for y := range h {
		r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
		fr, fg, fb := float64(r), float64(g), float64(bl)
		sR += fr
		s2R += fr * fr
		sG += fg
		s2G += fg * fg
		sB += fb
		s2B += fb * fb
	}
	n := float64(h)
	vR := s2R/n - (sR/n)*(sR/n)
	vG := s2G/n - (sG/n)*(sG/n)
	vB := s2B/n - (sB/n)*(sB/n)
	return vR+vG+vB < colorVarThresh
}

func decodeImg(data []byte, mime string) (image.Image, error) {
	if strings.Contains(mime, "jpeg") || strings.Contains(mime, "jpg") {
		img, _, err := image.Decode(bytes.NewReader(data))
		return img, err
	}
	// PNG (default for AI-generated images) or WebP (via blank import).
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		img, _, err = image.Decode(bytes.NewReader(data))
	}
	return img, err
}
