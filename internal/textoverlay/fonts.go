package textoverlay

import (
	"fmt"
	"os"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// faceSet wraps a parsed font with a cache of size-specific faces and a glyph
// buffer for coverage checks. A nil faceSet means "font not loaded".
type faceSet struct {
	font *opentype.Font
	mu   sync.Mutex
	buf  sfnt.Buffer
	// faces memoizes one font.Face per integer em-size so repeated overlays at
	// the same size don't re-instantiate a rasterizer.
	faces map[int]font.Face
}

// newFaceSet parses a single-font (.ttf/.otf) or font-collection (.ttc) byte
// blob. Collections (common for system CJK fonts like PingFang.ttc) are not
// handled by opentype.Parse, so we fall back to ParseCollection and take the
// first face — enough for coverage + rendering of one weight.
func newFaceSet(data []byte) (*faceSet, error) {
	f, err := opentype.Parse(data)
	if err == nil {
		return &faceSet{font: f, faces: make(map[int]font.Face)}, nil
	}
	coll, cerr := opentype.ParseCollection(data)
	if cerr != nil {
		return nil, fmt.Errorf("parse font: %w", err)
	}
	cf, ferr := coll.Font(0)
	if ferr != nil {
		return nil, fmt.Errorf("parse font collection: %w", ferr)
	}
	return &faceSet{font: cf, faces: make(map[int]font.Face)}, nil
}

// face returns (creating + caching) a font.Face at the given pixel em size.
func (fs *faceSet) face(px float64) (font.Face, error) {
	key := int(px + 0.5)
	if key < 1 {
		key = 1
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fc, ok := fs.faces[key]; ok {
		return fc, nil
	}
	fc, err := opentype.NewFace(fs.font, &opentype.FaceOptions{
		Size:    float64(key),
		DPI:     72, // 1px == 1pt at 72 DPI, so Size is effectively pixels
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("new face: %w", err)
	}
	fs.faces[key] = fc
	return fc, nil
}

// covers reports whether the font has a glyph for r (GlyphIndex 0 == .notdef ==
// missing, the tofu box we must never render).
func (fs *faceSet) covers(r rune) bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	idx, err := fs.font.GlyphIndex(&fs.buf, r)
	return err == nil && idx != 0
}

// LoadFonts resolves the primary + fallback font faces. The primary font is the
// CJK-capable file vendored in the repo (configs/fonts/…), resolved from the
// OVERLAY_FONT env override first, else the given vendored path. The fallback is
// the always-embedded Go Bold face (ASCII + Latin coverage, redistributable).
// We deliberately do NOT probe host/system fonts — the vendored file is the
// single source of truth so behaviour is identical across every machine. When
// the vendored font can't be loaded the primary is nil and only ASCII/Latin text
// renders — a CJK overlay then fails validation rather than producing tofu.
func LoadFonts(vendoredPath string) (*Fonts, error) {
	fallback, err := newFaceSet(gobold.TTF)
	if err != nil {
		return nil, fmt.Errorf("load fallback font: %w", err)
	}
	fonts := &Fonts{fallback: fallback}

	// First explicit candidate that parses wins: env override, then the vendored
	// repo path. No system-font fallback by design.
	candidates := make([]string, 0, 2)
	if p := os.Getenv("OVERLAY_FONT"); p != "" {
		candidates = append(candidates, p)
	}
	if vendoredPath != "" {
		candidates = append(candidates, vendoredPath)
	}

	for _, path := range candidates {
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			continue
		}
		primary, perr := newFaceSet(data)
		if perr != nil {
			continue
		}
		fonts.primary = primary
		break
	}
	return fonts, nil
}

// pick returns the face set that covers r, preferring the primary (CJK) font,
// then the fallback. Returns nil when neither covers r.
func (f *Fonts) pick(r rune) *faceSet {
	if f.primary != nil && f.primary.covers(r) {
		return f.primary
	}
	if f.fallback != nil && f.fallback.covers(r) {
		return f.fallback
	}
	return nil
}

// firstUncovered returns the first rune in s that no loaded font covers, and
// ok=true when every rune is covered.
func (f *Fonts) firstUncovered(s string) (rune, bool) {
	for _, r := range s {
		if r == ' ' || r == '\t' {
			continue
		}
		if f.pick(r) == nil {
			return r, false
		}
	}
	return 0, true
}

// HasCJK reports whether a CJK-capable primary font is loaded. Callers use this
// only for diagnostics; rendering correctness relies on per-rune coverage.
func (f *Fonts) HasCJK() bool { return f != nil && f.primary != nil }
