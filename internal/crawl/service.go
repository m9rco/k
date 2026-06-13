// Package crawl fetches game-asset image previews by game name and files them
// into the workspace as assets (kind=crawled, source recorded in Meta). It
// mirrors the generation/video async pattern: a task is created and published to
// the SSE broker, the source is queried off the request goroutine, and each
// fetched preview is persisted. The image source is pluggable behind the Source
// interface; when none is configured the capability degrades gracefully.
package crawl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gameasset/internal/store"
	"gameasset/internal/transport"
)

// Result is one crawled image reference returned by a Source.
type Result struct {
	// URL is the image URL to fetch.
	URL string
	// Source is a human-readable origin label (recorded on the asset).
	Source string
	// Title is an optional caption for the image.
	Title string
}

// Source searches a backing catalog for a game's image previews. Implementations
// must be safe for concurrent use.
type Source interface {
	// Configured reports whether the source can run.
	Configured() bool
	// Search returns up to limit image references for the given game name.
	Search(ctx context.Context, game string, limit int) ([]Result, error)
}

// CrawlMeta is stored as an asset's Meta JSON so the UI can attribute origin.
type CrawlMeta struct {
	Source string `json:"source"`
	Title  string `json:"title,omitempty"`
	Game   string `json:"game,omitempty"`
}

// Params describes one crawl request from the agent.
type Params struct {
	SessionID string
	Game      string
	// Limit caps how many previews to fetch (defaults applied in Start).
	Limit int
}

// Service runs game-asset crawl tasks.
type Service struct {
	src      Source
	store    *store.Store
	broker   *transport.TaskBroker
	assetDir string
	client   *http.Client
	now      func() time.Time
	newID    func(prefix string) string
}

// NewService constructs the crawl service.
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

// Configured reports whether crawling is available.
func (s *Service) Configured() bool {
	return s.src != nil && s.src.Configured()
}

const defaultCrawlLimit = 8

// Start validates inputs, creates a task, and kicks off the async crawl. It
// returns an error (without a task) when unconfigured or the game name is empty.
func (s *Service) Start(ctx context.Context, p Params) (string, error) {
	if !s.Configured() {
		return "", fmt.Errorf("物料爬取暂未配置，暂不可用")
	}
	if p.Game == "" {
		return "", fmt.Errorf("crawl requires a game name")
	}
	if p.Limit <= 0 || p.Limit > 20 {
		p.Limit = defaultCrawlLimit
	}
	taskID := s.newID("task")
	now := s.now()
	rec := store.TaskRecord{
		ID:        taskID,
		SessionID: p.SessionID,
		Kind:      "crawl",
		Status:    "queued",
		Intent:    "crawl_game_assets",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.InsertTask(rec); err != nil {
		return "", err
	}
	s.broker.Publish(taskID, transport.EventTaskQueued, p.SessionID, map[string]string{"intent": "crawl_game_assets"})
	go s.run(context.WithoutCancel(ctx), taskID, p)
	return taskID, nil
}

// run executes the crawl pipeline: search, then fetch each preview, skipping
// failures per-item and reporting a clear result.
func (s *Service) run(ctx context.Context, taskID string, p Params) {
	s.setStatus(taskID, p.SessionID, "running", transport.EventTaskRunning, 15)

	results, err := s.src.Search(ctx, p.Game, p.Limit)
	if err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("搜索失败：%v", err))
		return
	}
	if len(results) == 0 {
		s.fail(taskID, p.SessionID, fmt.Sprintf("未找到《%s》的素材", p.Game))
		return
	}
	if err := os.MkdirAll(s.assetDir, 0o755); err != nil {
		s.fail(taskID, p.SessionID, fmt.Sprintf("mkdir: %v", err))
		return
	}

	var saved, skipped int
	for i, r := range results {
		data, mime, err := s.fetch(ctx, r.URL)
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
		meta, _ := json.Marshal(CrawlMeta{Source: r.Source, Title: r.Title, Game: p.Game})
		if err := s.store.InsertAsset(store.AssetRecord{
			ID:        assetID,
			SessionID: p.SessionID,
			Kind:      "crawled",
			Path:      path,
			Mime:      mime,
			Meta:      string(meta),
			CreatedAt: s.now(),
		}); err != nil {
			skipped++
			continue
		}
		saved++
		s.progress(taskID, p.SessionID, 15+(i+1)*80/len(results))
	}

	if saved == 0 {
		s.fail(taskID, p.SessionID, "全部素材抓取失败")
		return
	}
	now := s.now()
	_ = s.store.UpdateTask(store.TaskRecord{ID: taskID, SessionID: p.SessionID, Status: "done", Progress: 100, UpdatedAt: now})
	s.broker.Publish(taskID, transport.EventTaskDone, p.SessionID, map[string]any{
		"saved":   saved,
		"skipped": skipped,
		"game":    p.Game,
	})
}

// fetch downloads an image URL, returning its bytes and detected mime.
func (s *Service) fetch(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20)) // 20 MiB cap
	if err != nil {
		return nil, "", err
	}
	mime := resp.Header.Get("Content-Type")
	if mime == "" || mime[:5] != "image" {
		mime = "image/png"
	}
	return data, mime, nil
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
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".img"
	}
}
