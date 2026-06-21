package textoverlay

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"gameasset/internal/crop"
)

// Render composites the request's overlays onto the base image bytes and returns
// the encoded result (same mime as the source). It validates coverage first so a
// missing glyph fails loudly rather than rendering a tofu box.
func Render(srcData []byte, req Request, fonts *Fonts) ([]byte, string, error) {
	if fonts == nil {
		return nil, "", fmt.Errorf("textoverlay: fonts not loaded")
	}
	if err := req.validate(fonts); err != nil {
		return nil, "", err
	}
	srcImg, mime, err := crop.Decode(srcData)
	if err != nil {
		return nil, "", fmt.Errorf("textoverlay: decode source: %w", err)
	}
	// Copy into an RGBA canvas we can draw onto (the decoded image may be a
	// read-only format-specific type).
	b := srcImg.Bounds()
	canvas := image.NewRGBA(b)
	draw.Draw(canvas, b, srcImg, b.Min, draw.Src)

	imgW, imgH := b.Dx(), b.Dy()
	for _, o := range req.Overlays {
		if err := drawOverlay(canvas, o, imgW, imgH, req.SafeInsetFrac, fonts); err != nil {
			return nil, "", err
		}
	}
	out, outMime, err := crop.Encode(canvas, mime)
	if err != nil {
		return nil, "", fmt.Errorf("textoverlay: encode: %w", err)
	}
	return out, outMime, nil
}

// drawOverlay measures, positions, and rasterizes a single overlay (optional
// background plate + optional stroke + fill text) onto the canvas.
func drawOverlay(canvas *image.RGBA, o Overlay, imgW, imgH int, inset float64, fonts *Fonts) error {
	px := o.FontPx
	if px <= 0 {
		// Default em ≈ 6% of the smaller dimension, a legible headline size that
		// scales with the artwork.
		px = 0.06 * float64(min(imgW, imgH))
		if px < 12 {
			px = 12
		}
	}

	textW, textH, ascent := measureRunes(o.Text, px, fonts)
	padX, padY := o.PadPx, o.PadPx
	if (o.Background != nil && hasAlpha(o.Background)) && o.PadPx == 0 {
		padX = int(px * 0.45)
		padY = int(px * 0.28)
	}
	boxW := textW + 2*padX
	boxH := textH + 2*padY
	originX, originY := resolveAnchor(o, boxW, boxH, imgW, imgH, inset)

	// Background plate (CTA button / badge).
	if o.Background != nil && hasAlpha(o.Background) {
		rect := image.Rect(originX, originY, originX+boxW, originY+boxH)
		draw.Draw(canvas, rect, &image.Uniform{o.Background}, image.Point{}, draw.Over)
	}

	// Text baseline: inside the padded box, top-aligned.
	baseX := originX + padX
	baseY := originY + padY + ascent

	fill := o.Color
	if fill == nil || !hasAlpha(fill) {
		fill = color.White
	}

	// Optional stroke: redraw the string offset in 8 directions underneath the
	// fill so glyphs stay legible on busy artwork.
	if o.Stroke != nil && hasAlpha(o.Stroke) && o.StrokePx > 0 {
		for dy := -o.StrokePx; dy <= o.StrokePx; dy++ {
			for dx := -o.StrokePx; dx <= o.StrokePx; dx++ {
				if dx == 0 && dy == 0 {
					continue
				}
				drawRunes(canvas, o.Text, baseX+dx, baseY+dy, px, o.Stroke, fonts)
			}
		}
	}
	drawRunes(canvas, o.Text, baseX, baseY, px, fill, fonts)
	return nil
}

// measureRunes returns the total advance width, line height, and ascent of s at
// the given size, picking a covering face per rune (CJK vs Latin may differ).
func measureRunes(s string, px float64, fonts *Fonts) (w, h, ascent int) {
	var adv fixed.Int26_6
	for _, r := range s {
		fs := fonts.pick(r)
		if fs == nil {
			continue // validated earlier; defensive
		}
		face, err := fs.face(px)
		if err != nil {
			continue
		}
		a, ok := face.GlyphAdvance(r)
		if !ok {
			a = font.MeasureString(face, string(r))
		}
		adv += a
		m := face.Metrics()
		if hh := (m.Ascent + m.Descent).Ceil(); hh > h {
			h = hh
		}
		if as := m.Ascent.Ceil(); as > ascent {
			ascent = as
		}
	}
	return adv.Ceil(), h, ascent
}

// drawRunes rasterizes s at the baseline (x,y) in col, picking a covering face
// per rune and advancing the pen across mixed-script text.
func drawRunes(canvas *image.RGBA, s string, x, y int, px float64, col color.Color, fonts *Fonts) {
	pen := fixedPt(x, y)
	src := image.NewUniform(col)
	for _, r := range s {
		fs := fonts.pick(r)
		if fs == nil {
			continue
		}
		face, err := fs.face(px)
		if err != nil {
			continue
		}
		dr, mask, maskp, adv, ok := face.Glyph(pen, r)
		if ok {
			draw.DrawMask(canvas, dr, src, image.Point{}, mask, maskp, draw.Over)
		} else {
			adv, _ = face.GlyphAdvance(r)
		}
		pen.X += adv
	}
}

// hasAlpha reports whether c is non-transparent (its alpha channel is > 0).
func hasAlpha(c color.Color) bool {
	if c == nil {
		return false
	}
	_, _, _, a := c.RGBA()
	return a > 0
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = bytes.MinRead
