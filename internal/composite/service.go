package composite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"time"

	"gameasset/internal/imageopt"
	"gameasset/internal/store"
)

// idGen returns a unique id; injectable for deterministic tests.
type idGen func() string

// Service persists client-flattened composite images (拼接合成) as workspace
// assets. Flattening happens in the browser (canvas.toBlob); this service only
// validates and stores the bytes — pure I/O, no AI, no model inference. It is the
// deterministic counterpart of the AI extract_layer (抠图) intent: layers are cut
// out by the image model, then composited and exported here.
type Service struct {
	assetDir string
	store    *store.Store
	now      func() time.Time
	newID    idGen
}

// NewService constructs a composite persist service.
func NewService(assetDir string, st *store.Store, newID idGen) *Service {
	return &Service{
		assetDir: assetDir,
		store:    st,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    newID,
	}
}

// Meta is the asset metadata recorded for a composite product. SourceAssetIDs
// lists the layers that were flattened, so the timeline can label derivation
// ("拼自 图A+图B"). Via marks the deterministic compositing path.
type Meta struct {
	Via            string   `json:"via"`
	SourceAssetIDs []string `json:"sourceAssetIds,omitempty"`
}

// ViaComposite marks a product produced by the free-compositing canvas.
const ViaComposite = "composite"

// maxCompositeBytes bounds an accepted upload. Composites are source-sized PNGs;
// 32 MiB is generous headroom and a backstop against abuse.
const maxCompositeBytes = 32 << 20

// Result describes the persisted composite asset.
type Result struct {
	AssetID string `json:"assetId"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Mime    string `json:"mime"`
	Bytes   int    `json:"bytes"`
}

// Persist stores the flattened composite bytes as a session-scoped asset. The
// bytes must be a PNG or JPEG image; anything else (or an oversized/empty body)
// is rejected so a malformed upload never lands in the workspace. Lossless PNG
// optimization is applied (alpha preserved — pixels unchanged) reusing the same
// pipeline as crops and generations.
func (s *Service) Persist(sessionID string, data []byte, sourceAssetIDs []string, lossless bool) (Result, error) {
	if sessionID == "" {
		return Result{}, fmt.Errorf("session id required")
	}
	if len(data) == 0 {
		return Result{}, fmt.Errorf("empty image body")
	}
	if len(data) > maxCompositeBytes {
		return Result{}, fmt.Errorf("image too large (%d bytes, max %d)", len(data), maxCompositeBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("not a valid image: %w", err)
	}
	mime := ""
	ext := ""
	switch format {
	case "png":
		mime, ext = "image/png", ".png"
	case "jpeg":
		mime, ext = "image/jpeg", ".jpg"
	default:
		return Result{}, fmt.Errorf("unsupported image format %q (want png or jpeg)", format)
	}

	if err := os.MkdirAll(s.assetDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create asset dir: %w", err)
	}
	id := s.newID()
	path := filepath.Join(s.assetDir, id+ext)
	out := imageopt.Optimize(data, lossless && mime == "image/png")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return Result{}, fmt.Errorf("write composite file: %w", err)
	}

	// Parent links to the first source layer when present, mirroring how crops
	// link to their source so the timeline can render a derivation hint.
	parent := ""
	if len(sourceAssetIDs) > 0 {
		parent = sourceAssetIDs[0]
	}
	meta, _ := json.Marshal(Meta{Via: ViaComposite, SourceAssetIDs: sourceAssetIDs})
	now := s.now()
	if err := s.store.InsertAsset(store.AssetRecord{
		ID:        id,
		SessionID: sessionID,
		Kind:      "composite",
		Path:      path,
		Mime:      mime,
		Width:     cfg.Width,
		Height:    cfg.Height,
		ParentID:  parent,
		Meta:      string(meta),
		CreatedAt: now,
	}); err != nil {
		return Result{}, fmt.Errorf("persist composite asset: %w", err)
	}
	return Result{AssetID: id, Width: cfg.Width, Height: cfg.Height, Mime: mime, Bytes: len(out)}, nil
}
