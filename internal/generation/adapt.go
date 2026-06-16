package generation

import (
	"context"
	"fmt"
	"math"

	"gameasset/internal/config"
	"gameasset/internal/crop"
)

// ratioTolerance is the max relative aspect-ratio difference at which platform
// adaptation takes the deterministic crop fast path instead of an AI repaint.
// 4% tolerates pixel-rounding between equivalent ratios (e.g. 1280×720 vs
// 1920×1080, both 16:9) while still routing genuine reshapes (1:1 → 9:16) to AI.
const ratioTolerance = 0.04

// Cropper is the subset of *crop.Service the adaptation entry needs: catalog
// lookup (to route + fill the prompt's placement slots) and the deterministic
// crop fast path. Kept as an interface so generation does not hard-depend on a
// concrete crop service (and stays testable with a stub).
type Cropper interface {
	SizeSpec(sizeID string) (crop.SizeSpec, bool)
	CropToSizes(sessionID, sourceAssetID string, sizeIDs []string, lossless bool, opts crop.Options) ([]crop.CropResult, error)
}

// SetCropper installs the crop service used by platform adaptation's fast path
// and catalog lookup. Wired by main; leaving it unset disables adaptation.
func (s *Service) SetCropper(c Cropper) { s.cropper = c }

// AdaptVia labels how one size's adaptation was produced.
const (
	AdaptViaCrop = "crop" // deterministic fast path (ratio match)
	AdaptViaAI   = "ai"   // image-model repaint (ratio reshape)
)

// AdaptOutcome is the result of adapting one source image to one target size.
// Exactly one of AssetID (crop/reused — immediately available) or TaskID (ai —
// async, progress streams over SSE) is set.
type AdaptOutcome struct {
	SizeID  string `json:"sizeId"`
	Via     string `json:"via"`
	AssetID string `json:"assetId,omitempty"`
	TaskID  string `json:"taskId,omitempty"`
}

// AdaptToPlatform adapts one source image to each requested target size,
// choosing per size between a deterministic crop (when the source and target
// aspect ratios match within ratioTolerance and share orientation) and an AI
// repaint (when the ratio must change — crop would slice the subject out).
//
// Session-level dedup runs first per size: when this session already holds a
// product derived from this source at this size, that product is reused and no
// new work starts (covers cross-turn re-requests; backed by persisted assets so
// it survives restarts). The crop fast path is synchronous (asset ready on
// return); the AI path returns a task id whose progress streams over SSE.
func (s *Service) AdaptToPlatform(ctx context.Context, sessionID, sourceAssetID string, sizeIDs []string, lossless bool, override *config.ImageProviderConfig) ([]AdaptOutcome, error) {
	if s.cropper == nil {
		return nil, fmt.Errorf("platform adaptation unavailable: crop service not wired")
	}
	if sourceAssetID == "" {
		return nil, fmt.Errorf("adapt requires a source asset id")
	}
	if len(sizeIDs) == 0 {
		return nil, fmt.Errorf("adapt requires at least one target size id")
	}
	src, err := s.store.GetAsset(sessionID, sourceAssetID)
	if err != nil {
		return nil, err
	}
	if src == nil {
		return nil, fmt.Errorf("source asset %q not found in session", sourceAssetID)
	}

	// Validate every size up front (unknown / non-producible is a hard error, as
	// in crop) so a bad id fails the call rather than silently producing fewer.
	specs := make([]crop.SizeSpec, 0, len(sizeIDs))
	for _, id := range sizeIDs {
		spec, ok := s.cropper.SizeSpec(id)
		if !ok {
			return nil, fmt.Errorf("unknown size id %q", id)
		}
		if !spec.Producible {
			return nil, fmt.Errorf("size %q is not producible (e.g. a video spec)", id)
		}
		specs = append(specs, spec)
	}

	outcomes := make([]AdaptOutcome, 0, len(specs))
	for _, spec := range specs {
		// Route: ratio match → deterministic crop; else AI repaint.
		if aspectClose(src.Width, src.Height, spec.Width, spec.Height) {
			results, err := s.cropper.CropToSizes(sessionID, sourceAssetID, []string{spec.SizeID}, lossless, crop.Options{Mode: crop.ModeCover})
			if err != nil {
				return nil, fmt.Errorf("adapt %s via crop: %w", spec.SizeID, err)
			}
			if len(results) == 0 {
				return nil, fmt.Errorf("adapt %s via crop produced nothing", spec.SizeID)
			}
			outcomes = append(outcomes, AdaptOutcome{SizeID: spec.SizeID, Via: AdaptViaCrop, AssetID: results[0].AssetID})
			continue
		}

		// 3) AI repaint: re-compose for the new aspect ratio, preserving subject.
		taskID, err := s.Start(ctx, GenerateParams{
			SessionID:        sessionID,
			SourceAssetID:    sourceAssetID,
			Lossless:         lossless,
			ProviderOverride: override,
			AdaptChannelID:   spec.ChannelID,
			AdaptSizeID:      spec.SizeID,
			AdaptSizeName:    spec.SizeName,
			AdaptWidth:       spec.Width,
			AdaptHeight:      spec.Height,
			Slots: Slots{
				Kind:          EditAdaptPlatform,
				ChannelName:   spec.ChannelName,
				AssetTypeKey:  spec.AssetTypeKey,
				AssetTypeName: spec.AssetTypeName,
				Orientation:   spec.Orientation,
				TargetWidth:   spec.Width,
				TargetHeight:  spec.Height,
				SizeNote:      spec.Note,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("adapt %s via AI: %w", spec.SizeID, err)
		}
		outcomes = append(outcomes, AdaptOutcome{SizeID: spec.SizeID, Via: AdaptViaAI, TaskID: taskID})
	}
	return outcomes, nil
}

// aspectClose reports whether the source and target aspect ratios match within
// ratioTolerance AND share orientation — the condition for the deterministic
// crop fast path. Any zero dimension is treated as "not close" (route to AI).
func aspectClose(srcW, srcH, dstW, dstH int) bool {
	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return false
	}
	if orientationOf(srcW, srcH) != orientationOf(dstW, dstH) {
		return false
	}
	arSrc := float64(srcW) / float64(srcH)
	arDst := float64(dstW) / float64(dstH)
	return math.Abs(arSrc-arDst)/arDst <= ratioTolerance
}

// orientationOf classifies dimensions as landscape / portrait / square.
func orientationOf(w, h int) string {
	switch {
	case w > h:
		return "landscape"
	case h > w:
		return "portrait"
	default:
		return "square"
	}
}
