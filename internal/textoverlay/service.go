package textoverlay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gameasset/internal/crop"
	"gameasset/internal/imageopt"
	"gameasset/internal/store"
)

// Service composites text overlays onto a workspace image and persists the
// result as a new asset linked to its parent. It mirrors crop.Service's
// persistence shape (write file → InsertAsset) so overlay products behave like
// any other synchronous workspace asset.
type Service struct {
	assetDir string
	store    *store.Store
	fonts    *Fonts
	newID    func(string) string
	now      func() time.Time
}

// NewService builds an overlay service. fonts must be loaded (LoadFonts); a nil
// fonts disables overlay (Configured reports false). newID mints asset ids.
func NewService(assetDir string, st *store.Store, fonts *Fonts, newID func(string) string) *Service {
	return &Service{
		assetDir: assetDir,
		store:    st,
		fonts:    fonts,
		newID:    newID,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// Configured reports whether the service can render (fonts loaded, store wired).
func (s *Service) Configured() bool {
	return s != nil && s.fonts != nil && s.store != nil
}

// HasCJK reports whether the primary CJK font is available (diagnostics).
func (s *Service) HasCJK() bool { return s != nil && s.fonts != nil && s.fonts.HasCJK() }

// OverlayMeta records how an overlay product was produced (for the asset meta
// column), mirroring crop's CropMeta.
type OverlayMeta struct {
	SourceAssetID string `json:"source_asset_id"`
	Overlays      int    `json:"overlays"`
	Via           string `json:"via"`
}

// OverlayResult is the persisted product reference returned to the caller.
type OverlayResult struct {
	AssetID string `json:"asset_id"`
	Path    string `json:"path"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Mime    string `json:"mime"`
}

// Apply renders req's overlays onto the source asset and persists the composite
// as a new asset linked to sourceAssetID. It returns a descriptive error when
// the source is missing, the text is uncovered by available fonts (anti-tofu),
// or rendering fails — so the caller can surface honest feedback.
func (s *Service) Apply(sessionID, sourceAssetID string, req Request, lossless bool) (OverlayResult, error) {
	if !s.Configured() {
		return OverlayResult{}, fmt.Errorf("textoverlay: service not configured")
	}
	src, err := s.store.GetAsset(sessionID, sourceAssetID)
	if err != nil {
		return OverlayResult{}, err
	}
	if src == nil {
		return OverlayResult{}, fmt.Errorf("source asset %q not found in session", sourceAssetID)
	}
	data, err := os.ReadFile(src.Path)
	if err != nil {
		return OverlayResult{}, fmt.Errorf("read source file: %w", err)
	}

	outData, mime, err := Render(data, req, s.fonts)
	if err != nil {
		return OverlayResult{}, err
	}

	if err := os.MkdirAll(s.assetDir, 0o755); err != nil {
		return OverlayResult{}, fmt.Errorf("create asset dir: %w", err)
	}
	id := s.newID("asset")
	ext := ".png"
	if mime == "image/jpeg" {
		ext = ".jpg"
	}
	path := filepath.Join(s.assetDir, id+ext)
	optimized := imageopt.Optimize(outData, lossless)
	if err := os.WriteFile(path, optimized, 0o644); err != nil {
		return OverlayResult{}, fmt.Errorf("write overlay file: %w", err)
	}

	// Decode dimensions from the rendered (pre-optimize) bytes for the record.
	w, h := src.Width, src.Height
	if di, _, derr := crop.Decode(outData); derr == nil {
		b := di.Bounds()
		w, h = b.Dx(), b.Dy()
	}

	meta, _ := json.Marshal(OverlayMeta{SourceAssetID: sourceAssetID, Overlays: len(req.Overlays), Via: "overlay"})
	if err := s.store.InsertAsset(store.AssetRecord{
		ID:        id,
		SessionID: sessionID,
		Kind:      "overlay",
		Path:      path,
		Mime:      mime,
		Width:     w,
		Height:    h,
		ParentID:  sourceAssetID,
		Meta:      string(meta),
		CreatedAt: s.now(),
	}); err != nil {
		return OverlayResult{}, fmt.Errorf("persist overlay asset: %w", err)
	}
	return OverlayResult{AssetID: id, Path: path, Width: w, Height: h, Mime: mime}, nil
}
