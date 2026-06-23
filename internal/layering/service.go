package layering

import (
	"context"
	"fmt"
	"os"
	"time"

	"gameasset/internal/composite"
	applog "gameasset/internal/log"
	"gameasset/internal/store"
	"gameasset/internal/vision"
)

// detector enumerates the foreground subjects to split into layers. Implemented
// by *vision.SubjectDetector; kept as a local interface for testability.
type detector interface {
	Configured() bool
	DetectSubjects(ctx context.Context, img []byte, mime string) ([]vision.Subject, error)
}

// persister stores a cut-out subject layer as a session-scoped workspace asset.
// Implemented by *composite.Service, reused so the deterministically-cropped
// layers land through the same lossless-PNG pipeline (and "composite" asset kind)
// as the final composite export. Kept as a local interface for testability.
type persister interface {
	Persist(sessionID string, data []byte, sourceAssetIDs []string, lossless bool) (composite.Result, error)
}

// Service orchestrates a layer split (图层精修) WITHOUT any generative image model.
// It detects the foreground subjects (people + marketing copy only) in a source
// image and cuts each one out of the ORIGINAL pixels into its own layer — onto a
// transparent background via the detector's segmentation mask (a true 抠图), or as
// an opaque rectangle when no mask is available. Either way every subject layer's
// RGB comes straight from the source, so layers drop back onto a source-sized
// canvas in perfect register (no AI repaint → no misalignment, no missing edges,
// no halos). The original image itself is the locked background base. The split is
// synchronous: detect → cut → persist, with no generation task to await.
type Service struct {
	det      detector
	persist  persister
	store    *store.Store
	lossless bool
}

// NewService constructs a layering service.
func NewService(det detector, persist persister, st *store.Store) *Service {
	return &Service{det: det, persist: persist, store: st, lossless: true}
}

// Configured reports whether a layer split can run (a subject detector is wired).
func (s *Service) Configured() bool { return s != nil && s.det != nil && s.det.Configured() }

// Role marks what a produced layer is in the stack.
const (
	RoleBackground = "background"
	RoleSubject    = "subject"
)

// Layer is one produced layer in the split, bottom-first (background, then
// subjects in detection order). Box is the layer's normalized position in the
// source frame: the background spans the whole frame {0,0,1,1}; each subject
// layer carries the actual (clamped) crop box so the canvas places the cut-out
// sub-image back at its origin.
type Layer struct {
	AssetID string     `json:"assetId"`
	Role    string     `json:"role"`
	Desc    string     `json:"desc,omitempty"`
	Box     vision.Box `json:"box"`
}

// Result is the outcome of a split: the locked canvas size plus the ordered layers.
type Result struct {
	SourceAssetID string  `json:"sourceAssetId"`
	Width         int     `json:"width"`
	Height        int     `json:"height"`
	Layers        []Layer `json:"layers"`
}

// fullFrame is the background layer's box: the entire source frame.
var fullFrame = vision.Box{X: 0, Y: 0, W: 1, H: 1}

// Split runs the full analyze→crop pipeline synchronously and returns the
// produced layers. It fails with a clear message when the detector is not
// configured or no separable foreground subject is found (nothing to layer).
// The background layer is always the original source image (a fixed, faithful
// base); subject layers are verbatim crops of the original pixels.
func (s *Service) Split(ctx context.Context, sessionID, sourceAssetID string) (Result, error) {
	lg := applog.From(ctx)
	t0 := time.Now()
	lg.Info().Str("event", "layer.split.start").Str("session", sessionID).Str("source", sourceAssetID).Msg("图层精修开始")
	if !s.Configured() {
		return Result{}, fmt.Errorf("图层分割不可用：未配置主体检测视觉模型")
	}
	asset, err := s.store.GetAsset(sessionID, sourceAssetID)
	if err != nil {
		return Result{}, err
	}
	if asset == nil {
		return Result{}, fmt.Errorf("源图未找到")
	}
	img, err := os.ReadFile(asset.Path)
	if err != nil {
		return Result{}, fmt.Errorf("读取源图: %w", err)
	}
	lg.Info().Str("event", "layer.split.source_read").
		Int("bytes", len(img)).Str("mime", asset.Mime).Int("w", asset.Width).Int("h", asset.Height).
		Msg("源图已读取")

	// Detection is a synchronous vision-model call and the slow step (tens of
	// seconds, more when masks are requested). Log a marker right before it so a
	// "frozen" UI is visibly waiting HERE, not stuck elsewhere.
	lg.Info().Str("event", "layer.split.detect_begin").Str("model_call", "DetectSubjects").
		Msg("调用视觉模型检测前景主体（可能耗时数十秒）…")
	dt := time.Now()
	subjects, err := s.det.DetectSubjects(ctx, img, asset.Mime)
	if err != nil {
		lg.Error().Str("event", "layer.split.detect_failed").Dur("dur", time.Since(dt)).Err(err).Msg("主体检测失败")
		return Result{}, fmt.Errorf("主体检测失败: %w", err)
	}
	lg.Info().Str("event", "layer.split.detect_done").Dur("dur", time.Since(dt)).Int("count", len(subjects)).Msg("主体检测返回")
	if len(subjects) == 0 {
		return Result{}, fmt.Errorf("未检测到可分层的前景主体（人物或宣发文案）")
	}

	// Background layer (bottom, locked): the ORIGINAL image itself — a stable,
	// pixel-faithful base. No inpaint, no AI, no failure mode.
	layers := []Layer{{AssetID: sourceAssetID, Role: RoleBackground, Box: fullFrame}}

	// Subject layers: cut each detected subject out of the source and persist it.
	// When the detector returned a segmentation mask the subject is cut onto a
	// TRANSPARENT background (mask as alpha over the verbatim original pixels — a
	// true 抠图, no repaint); otherwise it falls back to an opaque rectangular crop.
	// A subject that yields a degenerate crop or fails to persist is skipped
	// (best-effort) so one bad box doesn't sink the whole split.
	for i, sub := range subjects {
		sl := lg.With().Int("idx", i).Str("desc", sub.Desc).Int("mask_bytes", len(sub.Mask)).Logger()
		data, box, ok, cerr := cropSubject(img, sub.Box, sub.Mask)
		if cerr != nil || !ok {
			sl.Warn().Str("event", "layer.split.crop_skip").Bool("ok", ok).Err(cerr).Msg("主体裁切跳过（退化框或解码失败）")
			continue
		}
		sl.Info().Str("event", "layer.split.crop_ok").Bool("has_mask", len(sub.Mask) > 0).Int("png_bytes", len(data)).
			Float64("box_w", box.W).Float64("box_h", box.H).Msg("主体已裁切")
		res, perr := s.persist.Persist(sessionID, data, []string{sourceAssetID}, s.lossless)
		if perr != nil || res.AssetID == "" {
			sl.Warn().Str("event", "layer.split.persist_skip").Err(perr).Msg("主体层落库失败，跳过")
			continue
		}
		sl.Info().Str("event", "layer.split.persist_ok").Str("asset", res.AssetID).Msg("主体层已落库")
		layers = append(layers, Layer{AssetID: res.AssetID, Role: RoleSubject, Desc: sub.Desc, Box: box})
	}

	if len(layers) < 2 {
		lg.Error().Str("event", "layer.split.no_layers").Dur("dur", time.Since(t0)).Msg("未能裁出任何主体图层")
		return Result{}, fmt.Errorf("未能裁出任何主体图层")
	}
	lg.Info().Str("event", "layer.split.done").Dur("dur", time.Since(t0)).
		Int("layers", len(layers)).Int("subjects", len(layers)-1).Msg("图层精修完成")
	return Result{SourceAssetID: sourceAssetID, Width: asset.Width, Height: asset.Height, Layers: layers}, nil
}
