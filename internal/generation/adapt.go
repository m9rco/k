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

// AdaptToPlatform adapts an ordered reference group to each requested target
// size, producing EXACTLY ONE product per target size (product count = size
// count, never references × sizes). references[0] is the anchor — the content /
// subject / intent truth source that drives parent linkage, size inheritance and
// palette; references[1:] are auxiliary (style/element only, must not replace the
// anchor subject). The legacy single-image call passes a one-element slice.
//
// Routing per size:
//   - reference group of ≥2 → ALWAYS AI repaint. The deterministic crop fast path
//     can only act on one image, so it would silently drop the auxiliary
//     references; only an AI repaint lets the whole group inform the composition.
//   - single reference → the original ratio-based split: crop fast path when the
//     anchor and target aspect ratios match within ratioTolerance and share
//     orientation, else AI repaint.
//
// The crop fast path is synchronous (asset ready on return); the AI path returns
// a task id whose progress streams over SSE.
func (s *Service) AdaptToPlatform(ctx context.Context, sessionID string, references []string, sizeIDs []string, lossless bool, override *config.ImageProviderConfig, themeReport string) ([]AdaptOutcome, error) {
	if s.cropper == nil {
		return nil, fmt.Errorf("platform adaptation unavailable: crop service not wired")
	}
	// Normalize the reference group: drop blanks, cap at MaxReferenceImages, take
	// the first as anchor. An empty group is a hard error (nothing to adapt).
	refs := make([]string, 0, len(references))
	for _, id := range references {
		if id != "" {
			refs = append(refs, id)
		}
	}
	if len(refs) > MaxReferenceImages {
		refs = refs[:MaxReferenceImages]
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("adapt requires at least one source/reference asset id")
	}
	anchorID := refs[0]
	multiRef := len(refs) >= 2
	if len(sizeIDs) == 0 {
		return nil, fmt.Errorf("adapt requires at least one target size id")
	}
	src, err := s.store.GetAsset(sessionID, anchorID)
	if err != nil {
		return nil, err
	}
	if src == nil {
		return nil, fmt.Errorf("anchor asset %q not found in session", anchorID)
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
		// Route: single reference + ratio match → deterministic crop; a reference
		// group (≥2) always takes AI repaint so the auxiliary refs aren't dropped.
		if !multiRef && aspectClose(src.Width, src.Height, spec.Width, spec.Height) {
			results, err := s.cropper.CropToSizes(sessionID, anchorID, []string{spec.SizeID}, lossless, crop.Options{Mode: crop.ModeCover})
			if err != nil {
				return nil, fmt.Errorf("adapt %s via crop: %w", spec.SizeID, err)
			}
			if len(results) == 0 {
				return nil, fmt.Errorf("adapt %s via crop produced nothing", spec.SizeID)
			}
			outcomes = append(outcomes, AdaptOutcome{SizeID: spec.SizeID, Via: AdaptViaCrop, AssetID: results[0].AssetID})
			continue
		}

		// AI repaint: re-compose for the new aspect ratio, preserving the anchor
		// subject. The whole reference group is threaded through so multi-image
		// adaptations feed every reference to the image model.
		taskID, err := s.Start(ctx, GenerateParams{
			SessionID:         sessionID,
			ReferenceAssetIDs: refs,
			Lossless:          lossless,
			ProviderOverride:  override,
			AdaptChannelID:    spec.ChannelID,
			AdaptSizeID:       spec.SizeID,
			AdaptSizeName:     spec.SizeName,
			AdaptWidth:        spec.Width,
			AdaptHeight:       spec.Height,
			AdaptConvergeMode: spec.ConvergeMode,
			Slots: Slots{
				Kind:          EditAdaptPlatform,
				ChannelName:   spec.ChannelName,
				AssetTypeKey:  spec.AssetTypeKey,
				AssetTypeName: spec.AssetTypeName,
				Orientation:   spec.Orientation,
				TargetWidth:   spec.Width,
				TargetHeight:  spec.Height,
				SizeNote:      spec.Note,
				ThemeReport:   themeReport,
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

// convergeTolerance is the max absolute log-ratio difference between the AI
// product's aspect ratio and the target size's at which adaptation converges by
// a clean rescale (scale) rather than reshaping. ~0.18 ≈ a 20% ratio gap:
// within it the stretch is imperceptible. Beyond it (e.g. a 2:1 product forced
// toward a 4:1 banner) scaling would visibly distort and padding would leave
// large empty bands, so convergence routes to an AI outpaint instead — extend
// the scene to the new ratio, preserving the subject. Tuned against the
// gpt-image-2 size enum.
const convergeTolerance = 0.18

// convergeMode picks how an adaptation product is converged to the exact target
// size. A non-empty pin (from the size catalog) wins outright; otherwise the
// mode is auto-selected by the log-ratio gap between the provider output and the
// target. Any zero/invalid dimension falls back to scale (never crops blindly).
//
// Auto selection:
//   - gap ≤ convergeTolerance → ModeScale (imperceptible stretch to exact size)
//   - gap >  convergeTolerance → ModeOutpaint (AI extends the scene to the new
//     ratio; the generation service falls back to ModeContain when no outpainter
//     is wired, so a band-padded result is the worst case, never a sliced subject)
func convergeMode(pin string, genW, genH, dstW, dstH int) crop.Mode {
	switch crop.Mode(pin) {
	case crop.ModeContain:
		return crop.ModeContain
	case crop.ModeScale:
		return crop.ModeScale
	case crop.ModeCover:
		return crop.ModeCover
	case crop.ModeOutpaint:
		return crop.ModeOutpaint
	}
	if genW <= 0 || genH <= 0 || dstW <= 0 || dstH <= 0 {
		return crop.ModeScale
	}
	diff := math.Abs(math.Log(float64(genW)/float64(genH)) - math.Log(float64(dstW)/float64(dstH)))
	if diff > convergeTolerance {
		return crop.ModeOutpaint
	}
	return crop.ModeScale
}
