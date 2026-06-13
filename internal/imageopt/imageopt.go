// Package imageopt provides lossless image optimization. For PNG it re-encodes
// at maximum compression — pixel data is preserved exactly (true lossless),
// ancillary chunks are dropped, and the result is only used when it is actually
// smaller. Non-PNG inputs are returned untouched, and any failure falls back to
// the original bytes so optimization never breaks a产出.
package imageopt

import (
	"bytes"
	"image/png"
)

// pngMagic is the 8-byte PNG file signature.
var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

// IsPNG reports whether data carries the PNG signature.
func IsPNG(data []byte) bool {
	return len(data) >= 8 && bytes.Equal(data[:8], pngMagic)
}

// OptimizePNG losslessly re-encodes a PNG at best compression. The decoded
// pixels are identical to the input (lossless); only encoding overhead and
// ancillary chunks change. If the input is not a PNG, decoding fails, or the
// re-encoded result is not smaller, the original bytes are returned unchanged.
func OptimizePNG(data []byte) []byte {
	if !IsPNG(data) {
		return data
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return data // not decodable as PNG: leave as-is
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return data // re-encode failed: fall back to original
	}
	out := buf.Bytes()
	if len(out) >= len(data) {
		return data // no gain: keep original to never grow the file
	}
	return out
}

// Optimize applies lossless optimization when enabled and the format supports
// it. Currently only PNG is optimized; everything else passes through. A false
// `lossless` flag bypasses optimization entirely.
func Optimize(data []byte, lossless bool) []byte {
	if !lossless {
		return data
	}
	return OptimizePNG(data)
}
