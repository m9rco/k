// Package crop performs non-AI image resizing and cropping to platform target
// sizes. It uses a "cover-fit" strategy: the source is scaled to fully cover the
// target box, then center-cropped to the exact dimensions. This keeps the main
// subject (typically centered) visible while adapting between landscape and
// portrait aspect ratios without distortion.
package crop

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"

	xdraw "golang.org/x/image/draw"
)

// Result holds an encoded cropped image and its dimensions.
type Result struct {
	Data   []byte
	Width  int
	Height int
	Mime   string
}

// Mode selects the resize/crop strategy. The zero value (ModeCover) preserves
// the original "cover-fit, center-crop" behavior so callers that don't specify
// a mode are unchanged.
type Mode string

const (
	// ModeCover scales the source to fully cover the target box, then crops the
	// overflow according to the anchor (center by default).
	ModeCover Mode = "cover"
	// ModeContain scales the source to fully fit inside the target box without
	// cropping anything, padding the leftover area with a background color.
	ModeContain Mode = "contain"
	// ModeScale scales the source to exactly targetW×targetH without any padding
	// or cropping. Slight aspect-ratio distortion is accepted — use when source
	// and target ratios are very close (e.g. after the gpt-image-2 16px rounding)
	// and letterbox padding would be more objectionable than a <1% stretch.
	ModeScale Mode = "scale"
	// ModeAnchor behaves like cover but crops toward a nine-grid anchor instead
	// of center, keeping a chosen region (e.g. top) visible.
	ModeAnchor Mode = "anchor"
	// ModeRect crops a caller-supplied normalized region of the source first,
	// then scales that region to the target box (cover-fit within the rect).
	ModeRect Mode = "rect"
	// ModeOutpaint is NOT a pixel operation this package performs — CropImage
	// rejects it. It is a signal from platform adaptation's convergeMode that the
	// AI product's aspect ratio diverges so far from the target that padding would
	// leave large empty bands and cropping would slice the subject out; the right
	// move is an AI outpaint (extend the scene to the new ratio) handled in the
	// generation service, falling back to ModeContain when no outpainter is wired.
	// See generation.convergeMode and generation.Service.run.
	ModeOutpaint Mode = "outpaint"
)

// Anchor names a nine-grid crop position used by ModeAnchor (and ModeCover,
// where it defaults to AnchorCenter).
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

// Rect is a normalized crop region in [0,1] coordinates relative to the source
// image (X,Y = top-left corner; W,H = width/height fractions).
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Options carries the chosen mode and its parameters. The zero value is a valid
// center cover crop.
type Options struct {
	Mode   Mode
	Anchor Anchor
	// Rect is required (and only used) when Mode == ModeRect.
	Rect *Rect
	// Background fills the padded area in ModeContain. Nil means transparent
	// (which JPEG encoding flattens to white).
	Background color.Color
}

// anchorFraction maps an anchor to (fx, fy) ∈ [0,1] crop offsets, where 0 keeps
// the left/top edge and 1 keeps the right/bottom edge. Center is 0.5.
func anchorFraction(a Anchor) (float64, float64, bool) {
	switch a {
	case AnchorTopLeft:
		return 0, 0, true
	case AnchorTop:
		return 0.5, 0, true
	case AnchorTopRight:
		return 1, 0, true
	case AnchorLeft:
		return 0, 0.5, true
	case AnchorCenter, "":
		return 0.5, 0.5, true
	case AnchorRight:
		return 1, 0.5, true
	case AnchorBottomLeft:
		return 0, 1, true
	case AnchorBottom:
		return 0.5, 1, true
	case AnchorBottomRight:
		return 1, 1, true
	default:
		return 0, 0, false
	}
}

// CoverCrop scales src to cover a targetW×targetH box and center-crops it to
// exactly those dimensions. Returns the resulting image.
func CoverCrop(src image.Image, targetW, targetH int) (image.Image, error) {
	return coverCropAnchored(src, targetW, targetH, AnchorCenter)
}

// coverCropAnchored scales src to cover the target box, then crops the overflow
// toward the given anchor (center keeps the middle, top keeps the top, etc.).
func coverCropAnchored(src image.Image, targetW, targetH int, anchor Anchor) (image.Image, error) {
	if targetW <= 0 || targetH <= 0 {
		return nil, fmt.Errorf("invalid target size %dx%d", targetW, targetH)
	}
	sb := src.Bounds()
	srcW, srcH := sb.Dx(), sb.Dy()
	if srcW == 0 || srcH == 0 {
		return nil, fmt.Errorf("empty source image")
	}
	fx, fy, ok := anchorFraction(anchor)
	if !ok {
		return nil, fmt.Errorf("invalid anchor %q", anchor)
	}

	// Scale factor that makes the source cover the target in both dimensions.
	scaleX := float64(targetW) / float64(srcW)
	scaleY := float64(targetH) / float64(srcH)
	scale := scaleX
	if scaleY > scale {
		scale = scaleY
	}

	scaledW := int(float64(srcW)*scale + 0.5)
	scaledH := int(float64(srcH)*scale + 0.5)
	if scaledW < targetW {
		scaledW = targetW
	}
	if scaledH < targetH {
		scaledH = targetH
	}

	// High-quality scale into an intermediate image.
	scaled := image.NewRGBA(image.Rect(0, 0, scaledW, scaledH))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), src, sb, xdraw.Over, nil)

	// Crop the overflow toward the anchor.
	offX := int(float64(scaledW-targetW)*fx + 0.5)
	offY := int(float64(scaledH-targetH)*fy + 0.5)
	out := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	xdraw.Draw(out, out.Bounds(), scaled, image.Pt(offX, offY), xdraw.Src)
	return out, nil
}

// ContainCrop scales src to fully fit inside the target box without cropping,
// centering it and padding the leftover area with bg (nil = transparent).
func ContainCrop(src image.Image, targetW, targetH int, bg color.Color) (image.Image, error) {
	if targetW <= 0 || targetH <= 0 {
		return nil, fmt.Errorf("invalid target size %dx%d", targetW, targetH)
	}
	sb := src.Bounds()
	srcW, srcH := sb.Dx(), sb.Dy()
	if srcW == 0 || srcH == 0 {
		return nil, fmt.Errorf("empty source image")
	}

	// Scale factor that fits the source fully inside the target box.
	scaleX := float64(targetW) / float64(srcW)
	scaleY := float64(targetH) / float64(srcH)
	scale := scaleX
	if scaleY < scale {
		scale = scaleY
	}
	innerW := int(float64(srcW)*scale + 0.5)
	innerH := int(float64(srcH)*scale + 0.5)
	if innerW > targetW {
		innerW = targetW
	}
	if innerH > targetH {
		innerH = targetH
	}

	out := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	if bg != nil {
		xdraw.Draw(out, out.Bounds(), &image.Uniform{C: bg}, image.Point{}, xdraw.Src)
	}
	// Center the scaled source within the target box.
	offX := (targetW - innerW) / 2
	offY := (targetH - innerH) / 2
	dst := image.Rect(offX, offY, offX+innerW, offY+innerH)
	xdraw.CatmullRom.Scale(out, dst, src, sb, xdraw.Over, nil)
	return out, nil
}

// rectCrop crops a normalized region of src, then cover-crops that region to the
// target box (center anchor). The region must lie within [0,1] and be non-empty.
func rectCrop(src image.Image, targetW, targetH int, r Rect) (image.Image, error) {
	if r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1.0001 || r.Y+r.H > 1.0001 {
		return nil, fmt.Errorf("invalid rect region %+v", r)
	}
	sb := src.Bounds()
	srcW, srcH := sb.Dx(), sb.Dy()
	if srcW == 0 || srcH == 0 {
		return nil, fmt.Errorf("empty source image")
	}
	x0 := sb.Min.X + int(r.X*float64(srcW)+0.5)
	y0 := sb.Min.Y + int(r.Y*float64(srcH)+0.5)
	x1 := sb.Min.X + int((r.X+r.W)*float64(srcW)+0.5)
	y1 := sb.Min.Y + int((r.Y+r.H)*float64(srcH)+0.5)
	if x1 <= x0 || y1 <= y0 {
		return nil, fmt.Errorf("rect region collapses to empty crop")
	}
	if x1 > sb.Max.X {
		x1 = sb.Max.X
	}
	if y1 > sb.Max.Y {
		y1 = sb.Max.Y
	}
	// Extract the sub-image, then cover-crop it to the exact target box.
	sub := subImage(src, image.Rect(x0, y0, x1, y1))
	return coverCropAnchored(sub, targetW, targetH, AnchorCenter)
}

// subImage returns the region r of src as a standalone image. It uses the
// SubImage method when available (zero-copy) and otherwise copies pixels.
func subImage(src image.Image, r image.Rectangle) image.Image {
	if si, ok := src.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return si.SubImage(r)
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	xdraw.Draw(out, out.Bounds(), src, r.Min, xdraw.Src)
	return out
}

// CropImage applies opts to src, producing an image of exactly targetW×targetH.
func CropImage(src image.Image, targetW, targetH int, opts Options) (image.Image, error) {
	switch opts.Mode {
	case "", ModeCover:
		return coverCropAnchored(src, targetW, targetH, AnchorCenter)
	case ModeAnchor:
		anchor := opts.Anchor
		if anchor == "" {
			anchor = AnchorCenter
		}
		return coverCropAnchored(src, targetW, targetH, anchor)
	case ModeContain:
		return ContainCrop(src, targetW, targetH, opts.Background)
	case ModeScale:
		if targetW <= 0 || targetH <= 0 {
			return nil, fmt.Errorf("invalid target size %dx%d", targetW, targetH)
		}
		out := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
		xdraw.CatmullRom.Scale(out, out.Bounds(), src, src.Bounds(), xdraw.Over, nil)
		return out, nil
	case ModeRect:
		if opts.Rect == nil {
			return nil, fmt.Errorf("rect mode requires a region")
		}
		return rectCrop(src, targetW, targetH, *opts.Rect)
	default:
		return nil, fmt.Errorf("unknown crop mode %q", opts.Mode)
	}
}

// Decode decodes PNG or JPEG image bytes, returning the image and its mime type.
func Decode(data []byte) (image.Image, string, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode image: %w", err)
	}
	mime := "image/" + format
	return img, mime, nil
}

// Encode encodes img using the given mime type (defaults to PNG for unknown).
func Encode(img image.Image, mime string) ([]byte, string, error) {
	var buf bytes.Buffer
	switch {
	case strings.Contains(mime, "jpeg"), strings.Contains(mime, "jpg"):
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
			return nil, "", fmt.Errorf("encode jpeg: %w", err)
		}
		return buf.Bytes(), "image/jpeg", nil
	default:
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", fmt.Errorf("encode png: %w", err)
		}
		return buf.Bytes(), "image/png", nil
	}
}

// CropBytes is the one-shot helper: decode, cover-crop to target, re-encode in
// the source's format. Preserved for callers that don't need a specific mode.
func CropBytes(data []byte, targetW, targetH int) (Result, error) {
	return CropBytesWithOptions(data, targetW, targetH, Options{Mode: ModeCover})
}

// CropBytesWithOptions decodes the image, applies opts to produce a
// targetW×targetH image, and re-encodes in the source's format.
func CropBytesWithOptions(data []byte, targetW, targetH int, opts Options) (Result, error) {
	img, mime, err := Decode(data)
	if err != nil {
		return Result{}, err
	}
	cropped, err := CropImage(img, targetW, targetH, opts)
	if err != nil {
		return Result{}, err
	}
	out, outMime, err := Encode(cropped, mime)
	if err != nil {
		return Result{}, err
	}
	return Result{Data: out, Width: targetW, Height: targetH, Mime: outMime}, nil
}

// PadToAspectBytes centers the source on a transparent canvas whose aspect ratio
// equals targetW:targetH, padding the short axis with transparent bands while
// keeping the source at its native resolution (no scaling). The result is the
// EXACT input pixels plus empty margins on the two sides that must be filled —
// the canvas an AI outpainter extends into. Only ever expands the canvas along
// one axis (the one the target is "longer" on); the other axis matches the
// source. Returns the source unchanged when its ratio already matches the
// target within a hair (no useful band to fill).
//
// Example: a 1774×887 (2:1) product toward a 4:1 target yields a 3548×887 canvas
// with the product centered and ~887px transparent bands left and right.
func PadToAspectBytes(data []byte, targetW, targetH int) (Result, error) {
	if targetW <= 0 || targetH <= 0 {
		return Result{}, fmt.Errorf("invalid target size %dx%d", targetW, targetH)
	}
	img, mime, err := Decode(data)
	if err != nil {
		return Result{}, err
	}
	sb := img.Bounds()
	srcW, srcH := sb.Dx(), sb.Dy()
	if srcW == 0 || srcH == 0 {
		return Result{}, fmt.Errorf("empty source image")
	}

	srcAR := float64(srcW) / float64(srcH)
	dstAR := float64(targetW) / float64(targetH)

	// Decide the canvas: keep source pixels, extend only the axis the target is
	// proportionally longer on. The other dimension stays equal to the source.
	canvasW, canvasH := srcW, srcH
	switch {
	case dstAR > srcAR:
		// Target is wider → widen the canvas, keep height; bands left/right.
		canvasW = int(float64(srcH)*dstAR + 0.5)
	case dstAR < srcAR:
		// Target is taller → heighten the canvas, keep width; bands top/bottom.
		canvasH = int(float64(srcW)/dstAR + 0.5)
	default:
		// Ratios already match: nothing to outpaint, return source as-is.
		out, outMime, encErr := Encode(img, mime)
		if encErr != nil {
			return Result{}, encErr
		}
		return Result{Data: out, Width: srcW, Height: srcH, Mime: outMime}, nil
	}
	if canvasW < srcW {
		canvasW = srcW
	}
	if canvasH < srcH {
		canvasH = srcH
	}

	// Transparent canvas with the source centered. PNG output preserves alpha so
	// the outpainter sees exactly which region it must invent.
	out := image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
	offX := (canvasW - srcW) / 2
	offY := (canvasH - srcH) / 2
	dst := image.Rect(offX, offY, offX+srcW, offY+srcH)
	xdraw.Draw(out, dst, img, sb.Min, xdraw.Over)

	encoded, outMime, err := Encode(out, "image/png")
	if err != nil {
		return Result{}, err
	}
	return Result{Data: encoded, Width: canvasW, Height: canvasH, Mime: outMime}, nil
}

// ExtendEdgesBytes converges an image to the target aspect ratio purely by
// procedural edge extension — no AI, so it can never introduce a second subject,
// drift the style, or downscale the center. It keeps the source at native
// resolution centered on a canvas grown to the target ratio, then fills the
// extended margins by clamping each row/column's outermost pixels outward and
// softening that band with a light separable blur so the extension reads as a
// continuation of the scene's color/lighting rather than a hard pixel smear.
// The center source pixels are never touched. The canvas is finally cover-cropped
// to exactly targetW×targetH.
//
// This is the reliable fallback for extreme-ratio banners (e.g. a 3:1 master to a
// 4:1 placement): the two side bands become a soft extension of the master's edge
// tones — ideal for the copy-space margins a banner leaves blank anyway — while
// the centered subject and brand elements stay pixel-perfect.
func ExtendEdgesBytes(data []byte, targetW, targetH int) (Result, error) {
	if targetW <= 0 || targetH <= 0 {
		return Result{}, fmt.Errorf("invalid target size %dx%d", targetW, targetH)
	}
	img, _, err := Decode(data)
	if err != nil {
		return Result{}, err
	}
	sb := img.Bounds()
	srcW, srcH := sb.Dx(), sb.Dy()
	if srcW == 0 || srcH == 0 {
		return Result{}, fmt.Errorf("empty source image")
	}

	srcAR := float64(srcW) / float64(srcH)
	dstAR := float64(targetW) / float64(targetH)
	canvasW, canvasH := srcW, srcH
	if dstAR > srcAR {
		canvasW = int(float64(srcH)*dstAR + 0.5)
	} else if dstAR < srcAR {
		canvasH = int(float64(srcW)/dstAR + 0.5)
	}
	if canvasW < srcW {
		canvasW = srcW
	}
	if canvasH < srcH {
		canvasH = srcH
	}
	offX := (canvasW - srcW) / 2
	offY := (canvasH - srcH) / 2

	// Normalize the source to RGBA once for O(1) pixel reads.
	src := image.NewRGBA(image.Rect(0, 0, srcW, srcH))
	xdraw.Draw(src, src.Bounds(), img, sb.Min, xdraw.Src)
	at := func(x, y int) color.RGBA {
		// Clamp into the source: pixels outside extend the nearest edge (so the
		// horizontal bands continue each row's edge tone, the vertical bands each
		// column's — preserving the scene's top/bottom color distribution).
		if x < 0 {
			x = 0
		} else if x >= srcW {
			x = srcW - 1
		}
		if y < 0 {
			y = 0
		} else if y >= srcH {
			y = srcH - 1
		}
		i := src.PixOffset(x, y)
		s := src.Pix[i : i+4 : i+4]
		return color.RGBA{s[0], s[1], s[2], s[3]}
	}

	out := image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
	for cy := 0; cy < canvasH; cy++ {
		sy := cy - offY
		for cx := 0; cx < canvasW; cx++ {
			sx := cx - offX
			out.SetRGBA(cx, cy, at(sx, sy))
		}
	}

	// Soften only the extended margins (never the center) with a light box blur,
	// so the clamped streaks read as ambient background, not horizontal lines.
	blurMarginBands(out, offX, offY, srcW, srcH, canvasW, canvasH)

	final, err := CoverCrop(out, targetW, targetH)
	if err != nil {
		return Result{}, fmt.Errorf("cover to target: %w", err)
	}
	encoded, outMime, err := Encode(final, "image/png")
	if err != nil {
		return Result{}, err
	}
	return Result{Data: encoded, Width: targetW, Height: targetH, Mime: outMime}, nil
}

// blurMarginBands applies a box blur to only the extended-margin regions of dst
// (the area outside the centered source rect at offX,offY sized srcW×srcH),
// leaving the pristine center untouched. The blur radius scales with the band
// width so a wide extension is smoothed more. Reads from a snapshot so the blur
// is not self-reinforcing.
func blurMarginBands(dst *image.RGBA, offX, offY, srcW, srcH, canvasW, canvasH int) {
	leftBand := offX
	rightBand := canvasW - (offX + srcW)
	topBand := offY
	bottomBand := canvasH - (offY + srcH)
	maxBand := leftBand
	for _, b := range []int{rightBand, topBand, bottomBand} {
		if b > maxBand {
			maxBand = b
		}
	}
	if maxBand <= 0 {
		return
	}
	radius := maxBand / 12
	if radius < 2 {
		radius = 2
	}
	if radius > 24 {
		radius = 24
	}

	snap := image.NewRGBA(dst.Bounds())
	copy(snap.Pix, dst.Pix)
	inCenter := func(x, y int) bool {
		return x >= offX && x < offX+srcW && y >= offY && y < offY+srcH
	}
	boxAt := func(x, y int) color.RGBA {
		var r, g, b, a, n int
		for dy := -radius; dy <= radius; dy++ {
			yy := y + dy
			if yy < 0 || yy >= canvasH {
				continue
			}
			for dx := -radius; dx <= radius; dx++ {
				xx := x + dx
				if xx < 0 || xx >= canvasW {
					continue
				}
				i := snap.PixOffset(xx, yy)
				p := snap.Pix[i : i+4 : i+4]
				r += int(p[0])
				g += int(p[1])
				b += int(p[2])
				a += int(p[3])
				n++
			}
		}
		if n == 0 {
			return color.RGBA{}
		}
		return color.RGBA{uint8(r / n), uint8(g / n), uint8(b / n), uint8(a / n)}
	}
	for y := 0; y < canvasH; y++ {
		for x := 0; x < canvasW; x++ {
			if inCenter(x, y) {
				continue
			}
			dst.SetRGBA(x, y, boxAt(x, y))
		}
	}
}

// extended margins — preserving brand elements (logo, copy, subject, all centered
// in the master) at full master resolution while still filling the wider/taller
// frame.
//
// It first builds a canvas at the master's native resolution stretched to the
// target aspect ratio (so the center is never downscaled), scales the AI fill to
// cover that whole canvas as the background, then draws the master back over the
// center at 1:1. A `feather`-pixel alpha ramp on the master's outer edges blends
// the seam into the background so there is no hard line. The result is finally
// cover-cropped to exactly targetW×targetH.
//
// masterData is the high-res provider master; fillData is the outpainter's
// scene-extension render. feather is the blend width in master pixels (0 = hard
// seam). Falls back cleanly: if either decode fails the caller keeps the master.
func CompositeOutpaintBytes(masterData, fillData []byte, targetW, targetH, feather int) (Result, error) {
	if targetW <= 0 || targetH <= 0 {
		return Result{}, fmt.Errorf("invalid target size %dx%d", targetW, targetH)
	}
	master, _, err := Decode(masterData)
	if err != nil {
		return Result{}, fmt.Errorf("decode master: %w", err)
	}
	fill, _, err := Decode(fillData)
	if err != nil {
		return Result{}, fmt.Errorf("decode fill: %w", err)
	}
	mb := master.Bounds()
	mW, mH := mb.Dx(), mb.Dy()
	if mW == 0 || mH == 0 {
		return Result{}, fmt.Errorf("empty master image")
	}

	// Canvas keeps the master at native resolution and only grows the axis the
	// target is proportionally longer on, so the center is never downscaled.
	srcAR := float64(mW) / float64(mH)
	dstAR := float64(targetW) / float64(targetH)
	canvasW, canvasH := mW, mH
	if dstAR > srcAR {
		canvasW = int(float64(mH)*dstAR + 0.5)
	} else if dstAR < srcAR {
		canvasH = int(float64(mW)/dstAR + 0.5)
	}
	if canvasW < mW {
		canvasW = mW
	}
	if canvasH < mH {
		canvasH = mH
	}

	out := image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))

	// Background layer: AI fill cover-scaled over the whole canvas.
	bg, err := coverCropAnchored(fill, canvasW, canvasH, AnchorCenter)
	if err != nil {
		return Result{}, fmt.Errorf("scale fill: %w", err)
	}
	xdraw.Draw(out, out.Bounds(), bg, image.Point{}, xdraw.Src)

	// Master layer drawn 1:1 over the center, with a feathered alpha ramp on the
	// edges that border the extended margins so the seam blends into the fill.
	offX := (canvasW - mW) / 2
	offY := (canvasH - mH) / 2
	if feather < 0 {
		feather = 0
	}
	// Only feather the axis that was actually extended (the side that meets fill).
	featherX, featherY := 0, 0
	if canvasW > mW {
		featherX = feather
	}
	if canvasH > mH {
		featherY = feather
	}
	for y := 0; y < mH; y++ {
		for x := 0; x < mW; x++ {
			r, g, b, a := master.At(mb.Min.X+x, mb.Min.Y+y).RGBA()
			af := float64(a >> 8)
			// Edge ramp: scale alpha down within `feather` px of an extended edge.
			if featherX > 0 {
				if x < featherX {
					af *= float64(x) / float64(featherX)
				} else if x >= mW-featherX {
					af *= float64(mW-1-x) / float64(featherX)
				}
			}
			if featherY > 0 {
				if y < featherY {
					af *= float64(y) / float64(featherY)
				} else if y >= mH-featherY {
					af *= float64(mH-1-y) / float64(featherY)
				}
			}
			ma := uint8(af + 0.5)
			if ma == 0 {
				continue // fully transparent → keep background
			}
			// Source-over blend of the (alpha-scaled) master pixel onto the bg.
			cx, cy := offX+x, offY+y
			br, bgg, bb, _ := out.At(cx, cy).RGBA()
			fa := float64(ma) / 255.0
			blend := func(fg, bgc uint32) uint8 {
				return uint8(float64(fg>>8)*fa + float64(bgc>>8)*(1-fa) + 0.5)
			}
			out.SetRGBA(cx, cy, color.RGBA{
				R: blend(r, br),
				G: blend(g, bgg),
				B: blend(b, bb),
				A: 255,
			})
		}
	}

	// Converge the composited canvas to the exact target size.
	final, err := CoverCrop(out, targetW, targetH)
	if err != nil {
		return Result{}, fmt.Errorf("cover to target: %w", err)
	}
	encoded, outMime, err := Encode(final, "image/png")
	if err != nil {
		return Result{}, err
	}
	return Result{Data: encoded, Width: targetW, Height: targetH, Mime: outMime}, nil
}
