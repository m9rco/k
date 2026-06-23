package layering

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"gameasset/internal/generation"
	"gameasset/internal/store"
	"gameasset/internal/vision"
)

// detector enumerates the foreground subjects to split into layers. Implemented
// by *vision.SubjectDetector; kept as a local interface for testability.
type detector interface {
	Configured() bool
	DetectSubjects(ctx context.Context, img []byte, mime string) ([]vision.Subject, error)
}

// generator starts a generation task and is the layering service's only coupling
// to the generation pipeline. Implemented by *generation.Service.
type generator interface {
	Start(ctx context.Context, p generation.GenerateParams) (string, error)
}

// Service orchestrates a layer split (图层精修): it detects the foreground
// subjects in a source image, then drives the generation pipeline to (1) cut each
// subject onto a transparent layer (extract_layer, Gemini) and (2) inpaint a clean
// background (fill_background) as the locked base layer. The produced layers are
// all source-sized and positioned at the origin, so the compositing canvas stacks
// them directly at the original (fixed) dimensions.
type Service struct {
	det      detector
	gen      generator
	store    *store.Store
	lossless bool
	// awaitTimeout bounds how long Split waits for the spawned tasks. Tests shrink it.
	awaitTimeout time.Duration
	poll         time.Duration
}

// NewService constructs a layering service.
func NewService(det detector, gen generator, st *store.Store) *Service {
	return &Service{
		det:          det,
		gen:          gen,
		store:        st,
		lossless:     true,
		awaitTimeout: 180 * time.Second,
		poll:         2 * time.Second,
	}
}

// Configured reports whether a layer split can run (a subject detector is wired).
func (s *Service) Configured() bool { return s != nil && s.det != nil && s.det.Configured() }

// Role marks what a produced layer is in the stack.
const (
	RoleBackground = "background"
	RoleSubject    = "subject"
)

// Layer is one produced layer in the split, bottom-first (background, then
// subjects in detection order). All layers share the source dimensions.
type Layer struct {
	AssetID string `json:"assetId"`
	Role    string `json:"role"`
	Desc    string `json:"desc,omitempty"`
}

// Result is the outcome of a split: the locked canvas size plus the ordered layers.
type Result struct {
	SourceAssetID string  `json:"sourceAssetId"`
	Width         int     `json:"width"`
	Height        int     `json:"height"`
	Layers        []Layer `json:"layers"`
}

// Split runs the full analyze→split pipeline synchronously and returns the
// produced layers. It fails with a clear message when the detector is not
// configured or no separable foreground subject is found (nothing to layer).
func (s *Service) Split(ctx context.Context, sessionID, sourceAssetID string) (Result, error) {
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

	subjects, err := s.det.DetectSubjects(ctx, img, asset.Mime)
	if err != nil {
		return Result{}, fmt.Errorf("主体检测失败: %w", err)
	}
	if len(subjects) == 0 {
		return Result{}, fmt.Errorf("未检测到可独立分层的前景主体")
	}

	descs := make([]string, 0, len(subjects))
	for _, sub := range subjects {
		descs = append(descs, sub.Desc)
	}

	// Spawn the background inpaint + one cutout per subject, then await all
	// concurrently. A failed individual layer is dropped (best-effort) so a single
	// flaky generation doesn't sink the whole split; the background failing is fatal
	// (there'd be no base to composite onto).
	type spawn struct {
		taskID string
		role   string
		desc   string
	}
	var spawns []spawn

	bgTask, err := s.gen.Start(ctx, generation.GenerateParams{
		SessionID:     sessionID,
		SourceAssetID: sourceAssetID,
		Lossless:      s.lossless,
		Slots:         generation.Slots{Kind: generation.EditBackgroundFill, BackgroundDesc: strings.Join(descs, ", ")},
	})
	if err != nil {
		return Result{}, fmt.Errorf("启动背景层: %w", err)
	}
	spawns = append(spawns, spawn{taskID: bgTask, role: RoleBackground})

	for _, sub := range subjects {
		tid, err := s.gen.Start(ctx, generation.GenerateParams{
			SessionID:     sessionID,
			SourceAssetID: sourceAssetID,
			Lossless:      s.lossless,
			Slots:         generation.Slots{Kind: generation.EditExtractLayer, RegionDesc: sub.Desc},
		})
		if err != nil {
			continue // best-effort: skip a subject that failed to start
		}
		spawns = append(spawns, spawn{taskID: tid, role: RoleSubject, desc: sub.Desc})
	}

	// Await all spawned tasks concurrently.
	results := make([]Layer, len(spawns))
	var wg sync.WaitGroup
	for i, sp := range spawns {
		wg.Add(1)
		go func(i int, sp spawn) {
			defer wg.Done()
			rec := s.await(ctx, sessionID, sp.taskID)
			if rec != nil && rec.Status == "done" && rec.AssetID != "" {
				results[i] = Layer{AssetID: rec.AssetID, Role: sp.role, Desc: sp.desc}
			}
		}(i, sp)
	}
	wg.Wait()

	// Background layer: if the inpaint failed (transient provider hiccup like
	// Gemini "empty image data", or a safety block), fall back to the ORIGINAL
	// source image as the locked base. It is a valid opaque background — the
	// subjects simply composite over the unmodified original. This keeps a single
	// flaky background generation from sinking an otherwise-good split. (When the
	// fill succeeded, results[0] already holds the clean inpainted background.)
	if results[0].AssetID == "" {
		results[0] = Layer{AssetID: sourceAssetID, Role: RoleBackground}
	}

	layers := make([]Layer, 0, len(results))
	for _, r := range results {
		if r.AssetID != "" {
			layers = append(layers, r)
		}
	}
	if len(layers) < 2 {
		return Result{}, fmt.Errorf("未能成功抠出任何主体图层")
	}
	return Result{SourceAssetID: sourceAssetID, Width: asset.Width, Height: asset.Height, Layers: layers}, nil
}

// await polls the store for a spawned task until it reaches a terminal state or
// the timeout elapses. Returns nil on timeout/error so the caller drops the layer.
func (s *Service) await(ctx context.Context, sessionID, taskID string) *store.TaskRecord {
	ctx, cancel := context.WithTimeout(ctx, s.awaitTimeout)
	defer cancel()
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			rec, err := s.store.GetTask(sessionID, taskID)
			if err != nil || rec == nil {
				continue
			}
			if rec.Status == "done" || rec.Status == "failed" {
				return rec
			}
		}
	}
}
