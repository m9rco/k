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

// CoverCrop scales src to cover a targetW×targetH box and center-crops it to
// exactly those dimensions. Returns the resulting image.
func CoverCrop(src image.Image, targetW, targetH int) (image.Image, error) {
	if targetW <= 0 || targetH <= 0 {
		return nil, fmt.Errorf("invalid target size %dx%d", targetW, targetH)
	}
	sb := src.Bounds()
	srcW, srcH := sb.Dx(), sb.Dy()
	if srcW == 0 || srcH == 0 {
		return nil, fmt.Errorf("empty source image")
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

	// Center-crop the scaled image to the target box.
	offX := (scaledW - targetW) / 2
	offY := (scaledH - targetH) / 2
	out := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	xdraw.Draw(out, out.Bounds(), scaled, image.Pt(offX, offY), xdraw.Src)
	return out, nil
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
// the source's format.
func CropBytes(data []byte, targetW, targetH int) (Result, error) {
	img, mime, err := Decode(data)
	if err != nil {
		return Result{}, err
	}
	cropped, err := CoverCrop(img, targetW, targetH)
	if err != nil {
		return Result{}, err
	}
	out, outMime, err := Encode(cropped, mime)
	if err != nil {
		return Result{}, err
	}
	return Result{Data: out, Width: targetW, Height: targetH, Mime: outMime}, nil
}
