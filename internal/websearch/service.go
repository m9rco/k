package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gameasset/internal/store"
	"gameasset/internal/transport"
)

// ImageSearchParams describes one async image-search request.
type ImageSearchParams struct {
	SessionID string
	Query     string // Chinese keyword (Sogou)
	QueryEN   string // English keyword (Bing); empty for single-source mode
	Limit     int
}

// Service runs web-search tasks: synchronous text search and async image
// download into the workspace (mirrors the crawl.Service pattern).
type Service struct {
	src      Source
	store    *store.Store
	broker   *transport.TaskBroker
	assetDir string
	client   *http.Client
	now      func() time.Time
	newID    func(prefix string) string

	// announce, when set, broadcasts a task_created event over the conversation
	// channel the instant a search task is created, so the workspace paints the
	// per-image placeholders immediately (mirrors generation/video). Optional.
	announce TaskAnnouncer
}

// TaskAnnouncer broadcasts a task-created notice to a session's live clients.
// count is the number of images the search will download, so the frontend can
// pre-render that many placeholder slots (the "找几张就占几张" behavior).
type TaskAnnouncer interface {
	AnnounceTask(sessionID, taskID, kind string, count int)
}

// SetAnnouncer installs the task-created broadcaster (wired by main once the hub
// exists, avoiding an init cycle). Safe to leave unset.
func (s *Service) SetAnnouncer(a TaskAnnouncer) { s.announce = a }

// NewService constructs the web-search service.
func NewService(src Source, st *store.Store, broker *transport.TaskBroker, assetDir string, newID func(string) string) *Service {
	return &Service{
		src:      src,
		store:    st,
		broker:   broker,
		assetDir: assetDir,
		client:   &http.Client{Timeout: 30 * time.Second},
		now:      func() time.Time { return time.Now().UTC() },
		newID:    newID,
	}
}

// SearchText performs a synchronous web text search and returns up to limit results.
func (s *Service) SearchText(ctx context.Context, query string, limit int) ([]WebResult, error) {
	if limit <= 0 {
		limit = 5
	}
	return s.src.SearchWeb(ctx, query, limit)
}

// StartImageSearch creates a task, downloads matched images into the workspace,
// and returns the task id. Progress streams over SSE.
func (s *Service) StartImageSearch(ctx context.Context, p ImageSearchParams) (string, error) {
	if p.Limit <= 0 {
		p.Limit = 6
	}
	if p.Limit > 12 {
		p.Limit = 12
	}
	taskID := s.newID("wsearch")
	now := s.now()
	if err := s.store.InsertTask(store.TaskRecord{
		ID:        taskID,
		SessionID: p.SessionID,
		Kind:      "search", // distinct kind so the frontend classifies it as 搜图
		Status:    "queued",
		Intent:    "search_images:" + p.Query,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return "", fmt.Errorf("insert search task: %w", err)
	}
	// Announce over the conversation channel first (carrying the requested image
	// count) so the workspace pre-renders that many placeholder slots, then
	// publish the queued event on this task's SSE stream.
	if s.announce != nil {
		s.announce.AnnounceTask(p.SessionID, taskID, "search", p.Limit)
	}
	s.broker.Publish(taskID, transport.EventTaskQueued, p.SessionID, nil)
	go s.run(context.WithoutCancel(ctx), taskID, p)
	return taskID, nil
}

func (s *Service) run(ctx context.Context, taskID string, p ImageSearchParams) {
	s.setStatus(taskID, p.SessionID, "running", transport.EventTaskRunning, 10)

	results, err := s.src.SearchImages(ctx, p.Query, p.QueryEN, p.Limit)
	if err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("图片搜索失败：%v", err))
		return
	}
	if len(results) == 0 {
		s.fail(taskID, p.SessionID, "未找到相关图片")
		return
	}
	if err := os.MkdirAll(s.assetDir, 0o755); err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("mkdir: %v", err))
		return
	}

	var saved, skipped int
	for i, r := range results {
		data, mime, err := s.fetchImage(ctx, r.URL)
		if err != nil || len(data) == 0 {
			skipped++
			continue
		}
		assetID := s.newID("asset")
		path := filepath.Join(s.assetDir, assetID+extForMime(mime))
		if err := os.WriteFile(path, data, 0o644); err != nil {
			skipped++
			continue
		}
		meta, _ := json.Marshal(map[string]string{"source": "bing", "query": p.Query})
		if err := s.store.InsertAsset(store.AssetRecord{
			ID:        assetID,
			SessionID: p.SessionID,
			Kind:      "searched",
			Path:      path,
			Mime:      mime,
			// ParentID anchors every image in this batch to its search task, so the
			// timeline aggregates them into one 搜图 node regardless of how many
			// seconds the downloads span (it is a task id, not an asset id, so it
			// never renders a spurious "由 图N 加工" derivation label).
			ParentID:  taskID,
			Meta:      string(meta),
			CreatedAt: s.now(),
		}); err != nil {
			_ = os.Remove(path)
			skipped++
			continue
		}
		saved++
		pct := 10 + (i+1)*80/len(results)
		now := s.now()
		_ = s.store.UpdateTask(store.TaskRecord{ID: taskID, SessionID: p.SessionID, Status: "running", Progress: pct, UpdatedAt: now})
		s.broker.Publish(taskID, transport.EventTaskProgress, p.SessionID, map[string]any{
			"progress": pct,
			"asset_id": assetID, // triggers immediate workspace refresh in frontend
		})
	}

	if saved == 0 {
		s.fail(taskID, p.SessionID, "全部图片下载失败")
		return
	}
	now := s.now()
	_ = s.store.UpdateTask(store.TaskRecord{ID: taskID, SessionID: p.SessionID, Status: "done", Progress: 100, UpdatedAt: now})
	s.broker.Publish(taskID, transport.EventTaskDone, p.SessionID, map[string]any{"saved": saved, "skipped": skipped, "query": p.Query})
}

func (s *Service) fetchImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; GameAssetBot/1.0)")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, "", err
	}
	mime := resp.Header.Get("Content-Type")
	if i := strings.Index(mime, ";"); i > 0 {
		mime = mime[:i]
	}
	mime = strings.TrimSpace(mime)
	if !strings.HasPrefix(mime, "image/") {
		mime = "image/jpeg"
	}
	return data, mime, nil
}

func (s *Service) setStatus(taskID, sessionID, status string, ev transport.EventType, progress int) {
	now := s.now()
	_ = s.store.UpdateTask(store.TaskRecord{ID: taskID, SessionID: sessionID, Status: status, Progress: progress, UpdatedAt: now})
	s.broker.Publish(taskID, ev, sessionID, map[string]any{"progress": progress})
}

func (s *Service) fail(taskID, sessionID, msg string) {
	now := s.now()
	_ = s.store.UpdateTask(store.TaskRecord{ID: taskID, SessionID: sessionID, Status: "failed", Error: msg, UpdatedAt: now})
	s.broker.Publish(taskID, transport.EventTaskFailed, sessionID, map[string]any{"error": msg})
}

func extForMime(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".jpg"
	}
}
