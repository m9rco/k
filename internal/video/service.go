// Package video runs image-to-video generation tasks asynchronously, mirroring
// the generation package's pattern: a task is created and published to the SSE
// broker, the provider is invoked off the request goroutine, and the produced
// video is persisted as a workspace asset (kind=video). When no provider is
// configured the capability degrades gracefully — Start reports the feature is
// unavailable instead of failing opaquely.
package video

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gameasset/internal/store"
	"gameasset/internal/transport"
)

// Provider abstracts an image-to-video backend (happyhorse R2V).
type Provider interface {
	// Name identifies the provider for recording on assets.
	Name() string
	// Configured reports whether the provider has the credentials/model needed.
	Configured() bool
	// Generate produces a video (mp4 bytes) from a source image and motion prompt.
	Generate(ctx context.Context, req Request) (Output, error)
}

// Request is one image-to-video call.
type Request struct {
	// Prompt is the fully assembled, server-controlled motion prompt.
	Prompt string
	// SourceImage is the input frame the motion is applied to.
	SourceImage []byte
	SourceMime  string
}

// Output is the produced video.
type Output struct {
	Data     []byte
	Mime     string
	Provider string
}

// Params describes one image-to-video request from the agent.
type Params struct {
	SessionID string
	// SourceAssetID is the image the video is generated from (required).
	SourceAssetID string
	// Motion is the user's action description (sanitized before prompt assembly).
	Motion string
}

// Service runs image-to-video tasks.
type Service struct {
	prov     Provider
	store    *store.Store
	broker   *transport.TaskBroker
	assetDir string
	now      func() time.Time
	newID    func(prefix string) string
}

// NewService constructs the video service.
func NewService(prov Provider, st *store.Store, broker *transport.TaskBroker, assetDir string, newID func(string) string) *Service {
	return &Service{
		prov:     prov,
		store:    st,
		broker:   broker,
		assetDir: assetDir,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    newID,
	}
}

// Configured reports whether image-to-video is available.
func (s *Service) Configured() bool {
	return s.prov != nil && s.prov.Configured()
}

// Start validates inputs, creates a task, and kicks off async generation. It
// returns an error (without creating a task) when the provider is unconfigured
// or the source asset is missing, so the agent can relay a clear message.
func (s *Service) Start(ctx context.Context, p Params) (string, error) {
	if !s.Configured() {
		return "", fmt.Errorf("图生视频暂未配置，暂不可用")
	}
	if p.SourceAssetID == "" {
		return "", fmt.Errorf("image-to-video requires a source asset id")
	}
	if _, err := s.store.GetAsset(p.SessionID, p.SourceAssetID); err != nil {
		return "", err
	}
	taskID := s.newID("task")
	now := s.now()
	rec := store.TaskRecord{
		ID:        taskID,
		SessionID: p.SessionID,
		Kind:      "video",
		Status:    "queued",
		Intent:    "image_to_video",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.InsertTask(rec); err != nil {
		return "", err
	}
	s.broker.Publish(taskID, transport.EventTaskQueued, p.SessionID, map[string]string{"intent": "image_to_video"})
	go s.run(context.WithoutCancel(ctx), taskID, p)
	return taskID, nil
}

// run executes the video pipeline for a task.
func (s *Service) run(ctx context.Context, taskID string, p Params) {
	s.setStatus(taskID, p.SessionID, "running", transport.EventTaskRunning, 10)

	asset, err := s.store.GetAsset(p.SessionID, p.SourceAssetID)
	if err != nil || asset == nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("source asset not found: %v", err))
		return
	}
	srcBytes, err := os.ReadFile(asset.Path)
	if err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("read source: %v", err))
		return
	}
	s.progress(taskID, p.SessionID, 35)

	prompt := buildMotionPrompt(p.Motion)
	out, err := s.prov.Generate(ctx, Request{Prompt: prompt, SourceImage: srcBytes, SourceMime: asset.Mime})
	if err != nil {
		s.fail(taskID, p.SessionID, err.Error())
		return
	}
	s.progress(taskID, p.SessionID, 85)

	assetID := s.newID("asset")
	if err := os.MkdirAll(s.assetDir, 0o755); err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("mkdir: %v", err))
		return
	}
	mime := out.Mime
	if mime == "" {
		mime = "video/mp4"
	}
	path := filepath.Join(s.assetDir, assetID+extForMime(mime))
	if err := os.WriteFile(path, out.Data, 0o644); err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("write: %v", err))
		return
	}
	now := s.now()
	if err := s.store.InsertAsset(store.AssetRecord{
		ID:        assetID,
		SessionID: p.SessionID,
		Kind:      "video",
		Path:      path,
		Mime:      mime,
		Provider:  out.Provider,
		ParentID:  p.SourceAssetID,
		CreatedAt: now,
	}); err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("persist: %v", err))
		return
	}
	t := store.TaskRecord{ID: taskID, SessionID: p.SessionID, Status: "done", Progress: 100, AssetID: assetID, UpdatedAt: now}
	_ = s.store.UpdateTask(t)
	s.broker.Publish(taskID, transport.EventTaskDone, p.SessionID, map[string]string{
		"assetId":  assetID,
		"provider": out.Provider,
	})
}

// buildMotionPrompt wraps the sanitized motion description in a server template
// so user text cannot rewrite system behavior (injection defense).
func buildMotionPrompt(motion string) string {
	m := sanitizeMotion(motion)
	return "Animate the provided still image with the following motion, keeping the " +
		"subject and composition consistent: " + m
}

// sanitizeMotion strips control-style injection phrases and bounds length.
func sanitizeMotion(s string) string {
	s = strings.TrimSpace(s)
	low := strings.ToLower(s)
	for _, bad := range []string{"ignore previous", "you are now", "system:", "forget everything", "new instructions:"} {
		for {
			i := strings.Index(low, bad)
			if i < 0 {
				break
			}
			s = s[:i] + s[i+len(bad):]
			low = strings.ToLower(s)
		}
	}
	if len(s) > 500 {
		s = s[:500]
	}
	if strings.TrimSpace(s) == "" {
		s = "subtle natural motion"
	}
	return s
}

func (s *Service) setStatus(taskID, sessionID, status string, ev transport.EventType, progress int) {
	now := s.now()
	_ = s.store.UpdateTask(store.TaskRecord{ID: taskID, SessionID: sessionID, Status: status, Progress: progress, UpdatedAt: now})
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

func extForMime(mime string) string {
	switch mime {
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	default:
		return ".mp4"
	}
}
