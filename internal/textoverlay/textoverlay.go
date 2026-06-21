// Package textoverlay composites deterministic text and color blocks onto an
// existing image: CTA buttons, discount badges, launch-date headlines, brand
// labels. Rendering is done server-side with a real font rasterizer (NOT a
// generative model), so the output text is pixel-identical to the input string —
// no garbled glyphs, no misspellings, fully reproducible (text-overlay spec:
// 确定性文字/LOGO 叠加).
package textoverlay

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// Anchor is a nine-grid placement position for an overlay relative to the image
// (mirrors crop.Anchor's vocabulary so callers share one mental model).
type Anchor string

const (
	AnchorTopLeft     Anchor = "top-left"
	AnchorTop         Anchor = "top"
	AnchorTopRight    Anchor = "top-right"
	AnchorLeft        Anchor = "left"
	AnchorCenter      Anchor = "center"
	AnchorRight       Anchor = "right"
	AnchorBottomLeft  Anchor = "bottom-left"
	AnchorBottom      Anchor = "bottom"
	AnchorBottomRight Anchor = "bottom-right"
)

// Overlay is one text element to composite. Position is resolved from Anchor
// when set, else from normalized X/Y (0..1) of the element's top-left corner.
// Unset style fields fall back to deterministic, palette-agnostic defaults in
// the renderer.
type Overlay struct {
	// Text is the literal string to render. Treated purely as render content,
	// never as instructions (text-overlay: 叠加文本防注入).
	Text string
	// Anchor places the element in a nine-grid cell. When empty, X/Y are used.
	Anchor Anchor
	// X, Y are the normalized (0..1) top-left position used when Anchor is empty.
	X, Y float64
	// FontPx is the em size in pixels. 0 => a size proportional to the image.
	FontPx float64
	// Color is the text fill. Zero value (transparent) => opaque white default.
	Color color.Color
	// Stroke, when Alpha>0, outlines each glyph (improves legibility on busy art).
	Stroke color.Color
	// StrokePx is the outline width in pixels (default 0 = no stroke).
	StrokePx int
	// Background, when Alpha>0, draws a padded rounded-less rectangle behind the
	// text (a CTA "button" / badge plate).
	Background color.Color
	// PadPx is the background plate padding around the text (default proportional).
	PadPx int
}

// Request is a full overlay composite job over one base image.
type Request struct {
	// Overlays are drawn in order (later ones paint on top).
	Overlays []Overlay
	// SafeInsetFrac insets the usable area by this fraction of width/height on
	// every edge (e.g. 0.05). Anchored elements are kept inside the safe area so
	// platform crops never clip the text (text-overlay: 安全区内渲染).
	SafeInsetFrac float64
}

// Fonts bundles the resolved font faces used for rendering: a primary face
// (ideally CJK-capable) and a guaranteed-available fallback. Coverage is checked
// per-rune against both so an uncovered character fails loudly instead of
// rasterizing a tofu box (text-overlay: 缺字形回退).
type Fonts struct {
	primary  *faceSet
	fallback *faceSet
}

// validate checks the request is renderable: at least one overlay, every
// overlay has non-empty text, and every rune is covered by some loaded font.
// Returns a descriptive error naming the first uncovered character so the caller
// can surface an honest "can't render" signal rather than emit tofu.
func (r Request) validate(fonts *Fonts) error {
	if len(r.Overlays) == 0 {
		return fmt.Errorf("textoverlay: no overlays to render")
	}
	for i, o := range r.Overlays {
		if strings.TrimSpace(o.Text) == "" {
			return fmt.Errorf("textoverlay: overlay %d has empty text", i)
		}
		if bad, ok := fonts.firstUncovered(o.Text); !ok {
			return fmt.Errorf("textoverlay: character %q is not covered by any available font", string(bad))
		}
	}
	return nil
}

// resolveAnchor returns the top-left pixel origin for an overlay of the given
// rendered size (w,h) within an image of (imgW,imgH), honoring the safe inset.
func resolveAnchor(o Overlay, w, h, imgW, imgH int, inset float64) (int, int) {
	insetX := int(float64(imgW) * inset)
	insetY := int(float64(imgH) * inset)
	minX, minY := insetX, insetY
	maxX := imgW - insetX - w
	maxY := imgH - insetY - h
	if o.Anchor == "" {
		// Normalized free positioning, then clamp into the safe area.
		x := int(o.X * float64(imgW))
		y := int(o.Y * float64(imgH))
		return clamp(x, minX, maxX), clamp(y, minY, maxY)
	}
	cx := (minX + maxX) / 2
	cy := (minY + maxY) / 2
	var x, y int
	switch o.Anchor {
	case AnchorTopLeft:
		x, y = minX, minY
	case AnchorTop:
		x, y = cx, minY
	case AnchorTopRight:
		x, y = maxX, minY
	case AnchorLeft:
		x, y = minX, cy
	case AnchorCenter:
		x, y = cx, cy
	case AnchorRight:
		x, y = maxX, cy
	case AnchorBottomLeft:
		x, y = minX, maxY
	case AnchorBottom:
		x, y = cx, maxY
	case AnchorBottomRight:
		x, y = maxX, maxY
	default:
		x, y = cx, cy
	}
	return clamp(x, minX, maxX), clamp(y, minY, maxY)
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// measure returns the pixel width/height of s rendered with face.
func measure(face font.Face, s string) (int, int) {
	adv := font.MeasureString(face, s)
	m := face.Metrics()
	h := (m.Ascent + m.Descent).Ceil()
	return adv.Ceil(), h
}

// fixedPt builds a fixed.Point26_6 from integer pixel coordinates.
func fixedPt(x, y int) fixed.Point26_6 {
	return fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)}
}

// asImage asserts dst is a draw.Image (all decoded images we composite onto are
// copied into an *image.RGBA first, so this always succeeds in practice).
var _ = image.Rect
