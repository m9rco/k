package generation

import (
	"fmt"
	"image"
	"sort"

	"gameasset/internal/crop"
)

// PaletteColor is one dominant color with its share of sampled pixels.
type PaletteColor struct {
	Hex   string  `json:"hex"`
	Share float64 `json:"share"`
}

// ExtractPalette returns up to k dominant colors from the image, used as a
// color-harmony constraint for generation. Colors are quantized into coarse
// buckets so visually-similar pixels group together; buckets are ranked by
// pixel count. Fully transparent pixels are ignored.
func ExtractPalette(img image.Image, k int) []PaletteColor {
	if k <= 0 {
		k = 5
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w == 0 || h == 0 {
		return nil
	}

	// Sample at most ~64px per axis to keep extraction fast on large images.
	stepX := max1(w / 64)
	stepY := max1(h / 64)

	type bucket struct {
		count            int
		rSum, gSum, bSum int
	}
	buckets := make(map[int]*bucket)
	total := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y += stepY {
		for x := bounds.Min.X; x < bounds.Max.X; x += stepX {
			r16, g16, b16, a16 := img.At(x, y).RGBA()
			if a16 == 0 {
				continue
			}
			r, g, b := int(r16>>8), int(g16>>8), int(b16>>8)
			// Quantize to 5 bits per channel (bucket width 8) → 32 levels.
			key := (r>>3)<<10 | (g>>3)<<5 | (b >> 3)
			bk := buckets[key]
			if bk == nil {
				bk = &bucket{}
				buckets[key] = bk
			}
			bk.count++
			bk.rSum += r
			bk.gSum += g
			bk.bSum += b
			total++
		}
	}
	if total == 0 {
		return nil
	}

	type ranked struct {
		count   int
		r, g, b int
	}
	list := make([]ranked, 0, len(buckets))
	for _, bk := range buckets {
		list = append(list, ranked{
			count: bk.count,
			r:     bk.rSum / bk.count,
			g:     bk.gSum / bk.count,
			b:     bk.bSum / bk.count,
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].count > list[j].count })

	if k > len(list) {
		k = len(list)
	}
	out := make([]PaletteColor, 0, k)
	for i := 0; i < k; i++ {
		c := list[i]
		out = append(out, PaletteColor{
			Hex:   fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b),
			Share: float64(c.count) / float64(total),
		})
	}
	return out
}

// ExtractPaletteFromBytes decodes image bytes and extracts the palette.
func ExtractPaletteFromBytes(data []byte, k int) ([]PaletteColor, error) {
	img, _, err := crop.Decode(data)
	if err != nil {
		return nil, err
	}
	return ExtractPalette(img, k), nil
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
