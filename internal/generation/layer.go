package generation

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// chromaKeyToAlpha turns the extract_layer (抠图) product into a real
// transparent-background PNG by keying out the flat green fill the model was told
// to paint behind the subject.
//
// Why not just ask the model for "transparent"? Image models respond to "output
// a transparent PNG" by PAINTING a checkerboard (transparency as shown in
// editors) into the pixels — it looks like 马赛克 and carries no alpha. So the
// extract_layer prompt instead asks for a solid pure-green (#00FF00) background,
// which models honor reliably, and this function removes that green deterministically.
//
// Keying rule: a pixel becomes fully transparent when it is close to pure green —
// green dominant AND red/blue both low. A small tolerance absorbs JPEG ringing and
// anti-aliased edges; partially-green edge pixels get proportional alpha so the
// cutout edge stays smooth rather than jagged. On decode failure the original
// bytes are returned unchanged so a产出 is never lost.
func chromaKeyToAlpha(data []byte) ([]byte, string) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, ""
	}
	b := src.Bounds()
	out := image.NewNRGBA(b)
	draw.Draw(out, b, src, b.Min, draw.Src)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := out.PixOffset(x, y)
			r, g, bl := out.Pix[i], out.Pix[i+1], out.Pix[i+2]
			if a := keyAlpha(r, g, bl); a < 255 {
				out.Pix[i+3] = a
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return data, ""
	}
	return buf.Bytes(), "image/png"
}

// keyAlpha returns the alpha (0=fully keyed/transparent, 255=keep) for one RGB
// pixel against a pure-green key. It only keys pixels that are clearly green-
// dominant (g high, r and b low); colored subject pixels (including greens that
// also carry red/blue) are kept. Near-but-not-exact green produces partial alpha
// for a soft edge.
func keyAlpha(r, g, bl uint8) uint8 {
	// Greenness: how much green exceeds the larger of the other two channels.
	other := r
	if bl > other {
		other = bl
	}
	if g <= other {
		return 255 // not green-dominant → keep
	}
	greenness := int(g) - int(other)
	// Strong green dominance with low red/blue → fully keyed.
	const fullKey = 120 // greenness ≥ this and dark r/b → alpha 0
	const softKey = 40  // below this greenness → keep entirely
	if greenness <= softKey {
		return 255
	}
	if greenness >= fullKey {
		return 0
	}
	// Soft edge: ramp alpha down as greenness rises from softKey→fullKey.
	frac := float64(greenness-softKey) / float64(fullKey-softKey)
	return uint8((1 - frac) * 255)
}

// hasAlphaChannel reports whether the decoded image carries an alpha channel at
// all. Used by tests/diagnostics.
func hasAlphaChannel(img image.Image) bool {
	switch img.(type) {
	case *image.NRGBA, *image.NRGBA64, *image.RGBA, *image.RGBA64, *image.Alpha, *image.Alpha16:
		return true
	default:
		return false
	}
}

// pixelAt is a small helper for tests: the NRGBA tuple at (x,y).
func pixelAt(img image.Image, x, y int) color.NRGBA {
	c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
	return c
}
