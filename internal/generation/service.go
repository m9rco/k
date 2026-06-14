package generation

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "image/gif"  // register decoders for dimension probing
	_ "image/jpeg" // .
	_ "image/png"  // .

	"gameasset/internal/crop"
	"gameasset/internal/imageopt"
	"gameasset/internal/store"
	"gameasset/internal/transport"
)

// generator abstracts the failover generator for testability.
type generator interface {
	Generate(ctx context.Context, req Request) (Output, error)
}

// Service runs image-generation tasks asynchronously, publishing progress over
// SSE and persisting products as assets. Color adaptation (palette extraction)
// and prompt assembly happen here so every product is harmonized and every
// user input is sanitized.
type Service struct {
	gen      generator
	store    *store.Store
	broker   *transport.TaskBroker
	assetDir string
	now      func() time.Time
	newID    func(prefix string) string

	// announce, when set, broadcasts a task_created event over the conversation
	// channel the instant a task is created, so the workspace shows an immediate
	// placeholder without waiting for the agent turn to finish. Optional (nil in
	// tests / when no hub is wired).
	announce TaskAnnouncer

	// params caches each task's request so a failed product can be retried
	// without the caller re-supplying inputs (short-term in-memory store, D7).
	mu     sync.Mutex
	params map[string]GenerateParams
	// cancels holds the cancel func for each in-flight task's run context, so a
	// user-initiated cancel can abort the underlying provider HTTP request and
	// stop the pipeline before it persists an orphan product.
	cancels map[string]context.CancelFunc
}

// TaskAnnouncer broadcasts a task-created notice to a session's live clients.
// kind is one of "generate" / "video" so the frontend can pick a placeholder.
type TaskAnnouncer interface {
	AnnounceTask(sessionID, taskID, kind string)
}

// SetAnnouncer installs the task-created broadcaster (wired by main once the hub
// exists, avoiding an init cycle). Safe to leave unset.
func (s *Service) SetAnnouncer(a TaskAnnouncer) { s.announce = a }

// NewService constructs a generation service.
func NewService(gen generator, st *store.Store, broker *transport.TaskBroker, assetDir string, newID func(string) string) *Service {
	return &Service{
		gen:      gen,
		store:    st,
		broker:   broker,
		assetDir: assetDir,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    newID,
		params:   make(map[string]GenerateParams),
		cancels:  make(map[string]context.CancelFunc),
	}
}

// MaxReferenceImages bounds how many reference images one generation accepts.
const MaxReferenceImages = 6

// GenerateParams describes one generation request initiated by the agent.
type GenerateParams struct {
	SessionID string
	Slots     Slots
	// SourceAssetID, when set, is the existing asset to edit (二次调整 / 换背景).
	// Its bytes become the generation source and its palette drives harmony.
	// Treated as the primary reference when ReferenceAssetIDs is empty.
	SourceAssetID string
	// ReferenceAssetIDs lists reference assets to reuse composition/style from,
	// up to MaxReferenceImages (excess is truncated). The first id is the primary
	// reference (drives palette, size inheritance, and parent linkage); the rest
	// are additional references. When empty, SourceAssetID is used as the sole
	// reference (backward compatible).
	ReferenceAssetIDs []string
	// Lossless enables program-side PNG lossless optimization of the product
	// before persistence. Defaults to true at the call sites.
	Lossless bool
}

// primaryAndExtras resolves the effective reference list: the primary asset id
// (drives palette/size/parent) plus any additional reference ids, capped at
// MaxReferenceImages. Returns ("", nil) when there is no reference at all.
func (p GenerateParams) primaryAndExtras() (string, []string) {
	refs := p.ReferenceAssetIDs
	if len(refs) == 0 && p.SourceAssetID != "" {
		refs = []string{p.SourceAssetID}
	}
	if len(refs) > MaxReferenceImages {
		refs = refs[:MaxReferenceImages]
	}
	if len(refs) == 0 {
		return "", nil
	}
	return refs[0], refs[1:]
}

// Start creates a task, kicks off async generation, and returns the task id
// immediately. Progress is published to the broker; the produced asset id is
// attached to the terminal task_done event.
func (s *Service) Start(ctx context.Context, p GenerateParams) (string, error) {
	taskID := s.newID("task")
	now := s.now()
	rec := store.TaskRecord{
		ID:        taskID,
		SessionID: p.SessionID,
		Kind:      "generate",
		Status:    "queued",
		Intent:    string(p.Slots.Kind),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.InsertTask(rec); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.params[taskID] = p
	s.mu.Unlock()
	// Announce over the conversation channel first so the workspace can paint a
	// placeholder immediately, then publish the queued event on the SSE stream.
	if s.announce != nil {
		s.announce.AnnounceTask(p.SessionID, taskID, "generate")
	}
	s.broker.Publish(taskID, transport.EventTaskQueued, p.SessionID, map[string]string{"intent": string(p.Slots.Kind)})

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.cancels[taskID] = cancel
	s.mu.Unlock()
	go s.run(runCtx, taskID, p)
	return taskID, nil
}

// Retry re-runs a previously failed task in place, reusing its cached request
// parameters. Only failed tasks owned by the session can be retried; succeeded
// products are untouched (partial-retry requirement). The task id is preserved
// so the workspace placeholder updates rather than spawning a new card.
func (s *Service) Retry(ctx context.Context, sessionID, taskID string) error {
	rec, err := s.store.GetTask(sessionID, taskID)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("task %q not found", taskID)
	}
	if rec.Status != "failed" {
		return fmt.Errorf("task %q is not in a failed state (status=%s)", taskID, rec.Status)
	}
	s.mu.Lock()
	p, ok := s.params[taskID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("no cached parameters for task %q; cannot retry", taskID)
	}

	rec.Status = "queued"
	rec.Progress = 0
	rec.Error = ""
	rec.UpdatedAt = s.now()
	if err := s.store.UpdateTask(*rec); err != nil {
		return err
	}
	s.broker.Publish(taskID, transport.EventTaskQueued, sessionID, map[string]string{"intent": string(p.Slots.Kind), "retry": "true"})

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.cancels[taskID] = cancel
	s.mu.Unlock()
	go s.run(runCtx, taskID, p)
	return nil
}

// Cancel aborts an in-flight task: it fires the run context's cancel (which
// interrupts the provider HTTP request and stops the pipeline before it can
// persist an orphan product) and deletes the task record. Returns the number of
// task rows removed (0 if the task was unknown or already terminal). Session
// scoping is enforced via the store delete.
func (s *Service) Cancel(sessionID, taskID string) (int64, error) {
	s.mu.Lock()
	cancel := s.cancels[taskID]
	delete(s.cancels, taskID)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return s.store.DeleteTask(sessionID, taskID)
}

// run executes the generation pipeline for a task.
func (s *Service) run(ctx context.Context, taskID string, p GenerateParams) {
	// Drop the cancel entry once the pipeline ends (success, failure, or abort)
	// so the map doesn't leak and a stale cancel can't fire on a reused id.
	defer func() {
		s.mu.Lock()
		delete(s.cancels, taskID)
		s.mu.Unlock()
	}()
	s.setStatus(taskID, p.SessionID, "running", transport.EventTaskRunning, 10, "", "")

	// Resolve the primary reference (drives palette/size/parent) plus extras.
	primaryID, extraIDs := p.primaryAndExtras()

	// Load primary bytes + palette for color adaptation and size inheritance.
	var srcBytes []byte
	var srcMime string
	var palette []PaletteColor
	var srcW, srcH int
	if primaryID != "" {
		asset, err := s.store.GetAsset(p.SessionID, primaryID)
		if err != nil || asset == nil {
			s.fail(taskID, p.SessionID, fmt.Sprintf("source asset not found: %v", err))
			return
		}
		srcBytes, err = os.ReadFile(asset.Path)
		if err != nil {
			s.fail(taskID, p.SessionID, fmt.Sprintf("read source: %v", err))
			return
		}
		srcMime = asset.Mime
		srcW, srcH = asset.Width, asset.Height
		if pal, err := ExtractPaletteFromBytes(srcBytes, 5); err == nil {
			palette = pal
		}
	}

	// Load any additional reference images (best-effort: a missing/unreadable
	// extra reference is skipped rather than failing the whole generation).
	var extraImages [][]byte
	for _, id := range extraIDs {
		asset, err := s.store.GetAsset(p.SessionID, id)
		if err != nil || asset == nil {
			continue
		}
		b, err := os.ReadFile(asset.Path)
		if err != nil {
			continue
		}
		extraImages = append(extraImages, b)
	}
	s.progress(taskID, p.SessionID, 30)

	prompt, err := BuildPrompt(p.Slots, palette)
	if err != nil {
		s.fail(taskID, p.SessionID, err.Error())
		return
	}
	s.progress(taskID, p.SessionID, 45)

	// Desired output dimensions: 普通二次调整继承源图原尺寸；generate_icon 则使用目标
	// icon 尺寸（provider 端会 snap 到支持的尺寸 enum，最终再由 crop 收敛到精确尺寸）。
	wantW, wantH := srcW, srcH
	iconW, iconH := 0, 0
	if p.Slots.Kind == EditIcon {
		iconW, iconH = p.Slots.IconWidth, p.Slots.IconHeight
		if iconW <= 0 {
			iconW = DefaultIconSize
		}
		if iconH <= 0 {
			iconH = DefaultIconSize
		}
		wantW, wantH = iconW, iconH
	}

	genStart := time.Now()
	log.Printf("gen.run: task=%s calling provider.Generate (prompt %d chars, refs=%d)", taskID, len(prompt), len(extraImages))
	out, err := s.gen.Generate(ctx, Request{
		Prompt:          prompt,
		SourceImage:     srcBytes,
		SourceMime:      srcMime,
		ReferenceImages: extraImages,
		Width:           wantW,
		Height:          wantH,
	})
	if err != nil {
		log.Printf("gen.run: task=%s provider.Generate FAILED after %s: %v", taskID, time.Since(genStart), err)
		s.fail(taskID, p.SessionID, err.Error())
		return
	}
	log.Printf("gen.run: task=%s provider.Generate OK in %s (%d bytes)", taskID, time.Since(genStart), len(out.Data))

	// If the task was cancelled while the provider was working, drop the result
	// instead of persisting it — otherwise a cancelled task leaves an orphan
	// asset that resurfaces on the next workspace refresh.
	if ctx.Err() != nil {
		log.Printf("gen.run: task=%s cancelled, discarding product", taskID)
		return
	}
	s.progress(taskID, p.SessionID, 80)

	// generate_icon: provider 会把尺寸 snap 到支持的 enum，产物尺寸往往大于目标
	// icon 尺寸。用 contain 收敛到精确尺寸——保留完整主体、不裁切，多余区域留白
	// （透明），保证最终落库即是请求的 icon 尺寸。
	if p.Slots.Kind == EditIcon && iconW > 0 && iconH > 0 {
		if conv, err := crop.CropBytesWithOptions(out.Data, iconW, iconH, crop.Options{Mode: crop.ModeContain}); err != nil {
			log.Printf("gen.run: task=%s icon converge to %dx%d FAILED: %v (keeping provider output)", taskID, iconW, iconH, err)
		} else {
			out.Data = conv.Data
		}
	}

	// Persist the product.
	assetID := s.newID("asset")
	if err := os.MkdirAll(s.assetDir, 0o755); err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("mkdir: %v", err))
		return
	}
	path := filepath.Join(s.assetDir, assetID+".png")
	// Lossless PNG optimization before persistence (pixels unchanged).
	outData := imageopt.Optimize(out.Data, p.Lossless)
	if err := os.WriteFile(path, outData, 0o644); err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("write: %v", err))
		return
	}
	// Record the produced image's actual dimensions so二次调整产物尺寸可追溯。
	outW, outH := decodeDimensions(outData)
	now := s.now()
	if err := s.store.InsertAsset(store.AssetRecord{
		ID:        assetID,
		SessionID: p.SessionID,
		Kind:      "generated",
		Path:      path,
		Mime:      out.Mime,
		Width:     outW,
		Height:    outH,
		Provider:  out.Provider,
		ParentID:  primaryID,
		CreatedAt: now,
	}); err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("persist: %v", err))
		return
	}

	// Terminal success: attach the produced asset id.
	t := store.TaskRecord{ID: taskID, SessionID: p.SessionID, Status: "done", Progress: 100, AssetID: assetID, UpdatedAt: now}
	_ = s.store.UpdateTask(t)
	s.broker.Publish(taskID, transport.EventTaskDone, p.SessionID, map[string]string{
		"assetId":  assetID,
		"provider": out.Provider,
	})
	log.Printf("gen.run: task=%s DONE asset=%s published task_done", taskID, assetID)
}

func (s *Service) setStatus(taskID, sessionID, status string, ev transport.EventType, progress int, assetID, errMsg string) {
	now := s.now()
	_ = s.store.UpdateTask(store.TaskRecord{ID: taskID, SessionID: sessionID, Status: status, Progress: progress, AssetID: assetID, Error: errMsg, UpdatedAt: now})
	s.broker.Publish(taskID, ev, sessionID, map[string]any{"progress": progress})
}

func (s *Service) progress(taskID, sessionID string, pct int) {
	now := s.now()
	_ = s.store.UpdateTask(store.TaskRecord{ID: taskID, SessionID: sessionID, Status: "running", Progress: pct, UpdatedAt: now})
	s.broker.Publish(taskID, transport.EventTaskProgress, sessionID, map[string]int{"progress": pct})
}

func (s *Service) fail(taskID, sessionID, msg string) {
	now := s.now()
	_ = s.store.UpdateTask(store.TaskRecord{ID: taskID, SessionID: sessionID, Status: "failed", Error: msg, UpdatedAt: now})
	s.broker.Publish(taskID, transport.EventTaskFailed, sessionID, map[string]string{"error": msg})
}

// decodeDimensions reads an image's pixel dimensions; returns (0,0) if the
// bytes cannot be decoded, leaving width/height unset rather than failing.
func decodeDimensions(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}
