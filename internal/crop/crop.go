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
