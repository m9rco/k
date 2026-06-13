package crop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gameasset/internal/config"
	"gameasset/internal/store"
)

// idGen returns a unique id; injectable for deterministic tests.
type idGen func() string

// Service exposes the channel catalog and performs batch crops, persisting each
// product as an asset. It performs pure image processing only — no AI calls.
type Service struct {
	channels []config.Channel
	assetDir string
	store    *store.Store
	now      func() time.Time
	newID    idGen

	// byID is a flattened index of every size in the catalog, keyed by its
	// globally-unique id, with the owning channel recorded for result labelling.
	byID map[string]sizeRef
}

// sizeRef is a catalog size plus the id of the channel it belongs to.
type sizeRef struct {
	size      config.Size
	channelID string
}

// NewService constructs a crop service over the channel catalog.
func NewService(channels []config.Channel, assetDir string, st *store.Store, newID idGen) *Service {
	s := &Service{
		channels: channels,
		assetDir: assetDir,
		store:    st,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    newID,
	}
	s.byID = make(map[string]sizeRef)
	for _, ch := range channels {
		for _, at := range ch.AssetTypes {
			for _, sz := range at.Sizes {
				if sz.ID == "" {
					continue
				}
				s.byID[sz.ID] = sizeRef{size: sz, channelID: ch.ID}
			}
		}
	}
	return s
}

// Channels returns the configured channel catalog for the frontend to render as
// a layered (channel → asset type → size) selector.
func (s *Service) Channels() []config.Channel {
	return s.channels
}

// CropResult describes one produced crop asset.
type CropResult struct {
	AssetID   string `json:"assetId"`
	SizeID    string `json:"sizeId"`
	ChannelID string `json:"channelId"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Mime      string `json:"mime"`
	// Bytes is the produced file size, so callers can surface a non-blocking
	// hint when it exceeds the size's maxKB constraint (never enforced here).
	Bytes int `json:"bytes"`
}

// CropMeta is the JSON payload stored on a cropped asset's Meta field so
// downstream features (zip packaging) can organize products by channel/size.
type CropMeta struct {
	ChannelID string `json:"channelId"`
	SizeID    string `json:"sizeId"`
	SizeName  string `json:"sizeName"`
}

// CropToSizes crops the source asset to each requested size id, persisting every
// product as a "cropped" asset owned by the session. The source is loaded from
// the store (enforcing session ownership). Sizes are looked up by their globally
// unique id across the whole channel catalog.
func (s *Service) CropToSizes(sessionID, sourceAssetID string, sizeIDs []string) ([]CropResult, error) {
	src, err := s.store.GetAsset(sessionID, sourceAssetID)
	if err != nil {
		return nil, err
	}
	if src == nil {
		return nil, fmt.Errorf("source asset %q not found in session", sourceAssetID)
	}
	data, err := os.ReadFile(src.Path)
	if err != nil {
		return nil, fmt.Errorf("read source file: %w", err)
	}

	refs, err := s.resolveSizeIDs(sizeIDs)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(s.assetDir, 0o755); err != nil {
		return nil, fmt.Errorf("create asset dir: %w", err)
	}

	var results []CropResult
	for _, ref := range refs {
		sz := ref.size
		res, err := CropBytes(data, sz.Width, sz.Height)
		if err != nil {
			return nil, fmt.Errorf("crop to %s: %w", sz.ID, err)
		}
		id := s.newID()
		ext := ".png"
		if res.Mime == "image/jpeg" {
			ext = ".jpg"
		}
		path := filepath.Join(s.assetDir, id+ext)
		if err := os.WriteFile(path, res.Data, 0o644); err != nil {
			return nil, fmt.Errorf("write crop file: %w", err)
		}
		now := s.now()
		meta, _ := json.Marshal(CropMeta{ChannelID: ref.channelID, SizeID: sz.ID, SizeName: sz.Name})
		if err := s.store.InsertAsset(store.AssetRecord{
			ID:        id,
			SessionID: sessionID,
			Kind:      "cropped",
			Path:      path,
			Mime:      res.Mime,
			Width:     res.Width,
			Height:    res.Height,
			ParentID:  sourceAssetID,
			Meta:      string(meta),
			CreatedAt: now,
		}); err != nil {
			return nil, fmt.Errorf("persist crop asset: %w", err)
		}
		results = append(results, CropResult{
			AssetID: id, SizeID: sz.ID, ChannelID: ref.channelID, Path: path,
			Name: sz.Name, Width: res.Width, Height: res.Height,
			Mime: res.Mime, Bytes: len(res.Data),
		})
	}
	return results, nil
}

// resolveSizeIDs maps requested size ids to their catalog entries. An unknown id
// or a non-producible size (e.g. a video spec) is a hard error so callers get
// explicit feedback rather than silently producing fewer crops.
func (s *Service) resolveSizeIDs(ids []string) ([]sizeRef, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("no target size ids supplied")
	}
	out := make([]sizeRef, 0, len(ids))
	for _, id := range ids {
		ref, ok := s.byID[id]
		if !ok {
			return nil, fmt.Errorf("unknown size id %q", id)
		}
		if !ref.size.Producible {
			return nil, fmt.Errorf("size %q is not producible by cropping", id)
		}
		out = append(out, ref)
	}
	return out, nil
}
