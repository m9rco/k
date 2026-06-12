package generation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

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

	// params caches each task's request so a failed product can be retried
	// without the caller re-supplying inputs (short-term in-memory store, D7).
	mu     sync.Mutex
	params map[string]GenerateParams
}

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
	}
}

// GenerateParams describes one generation request initiated by the agent.
type GenerateParams struct {
	SessionID string
	Slots     Slots
	// SourceAssetID, when set, is the existing asset to edit (二次调整 / 换背景).
	// Its bytes become the generation source and its palette drives harmony.
	SourceAssetID string
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
	s.broker.Publish(taskID, transport.EventTaskQueued, p.SessionID, map[string]string{"intent": string(p.Slots.Kind)})

	go s.run(context.WithoutCancel(ctx), taskID, p)
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

	go s.run(context.WithoutCancel(ctx), taskID, p)
	return nil
}

// run executes the generation pipeline for a task.
func (s *Service) run(ctx context.Context, taskID string, p GenerateParams) {
	s.setStatus(taskID, p.SessionID, "running", transport.EventTaskRunning, 10, "", "")

	// Load source bytes + palette for color adaptation.
	var srcBytes []byte
	var srcMime string
	var palette []PaletteColor
	if p.SourceAssetID != "" {
		asset, err := s.store.GetAsset(p.SessionID, p.SourceAssetID)
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
		if pal, err := ExtractPaletteFromBytes(srcBytes, 5); err == nil {
			palette = pal
		}
	}
	s.progress(taskID, p.SessionID, 30)

	prompt, err := BuildPrompt(p.Slots, palette)
	if err != nil {
		s.fail(taskID, p.SessionID, err.Error())
		return
	}
	s.progress(taskID, p.SessionID, 45)

	out, err := s.gen.Generate(ctx, Request{
		Prompt:      prompt,
		SourceImage: srcBytes,
		SourceMime:  srcMime,
	})
	if err != nil {
		s.fail(taskID, p.SessionID, err.Error())
		return
	}
	s.progress(taskID, p.SessionID, 80)

	// Persist the product.
	assetID := s.newID("asset")
	if err := os.MkdirAll(s.assetDir, 0o755); err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("mkdir: %v", err))
		return
	}
	path := filepath.Join(s.assetDir, assetID+".png")
	if err := os.WriteFile(path, out.Data, 0o644); err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("write: %v", err))
		return
	}
	now := s.now()
	if err := s.store.InsertAsset(store.AssetRecord{
		ID:        assetID,
		SessionID: p.SessionID,
		Kind:      "generated",
		Path:      path,
		Mime:      out.Mime,
		Provider:  out.Provider,
		ParentID:  p.SourceAssetID,
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
