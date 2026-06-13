// Package workspace exposes the operable asset workspace: it lists a session's
// assets and tasks (so the frontend can render product cards and placeholders
// with live status), accepts image uploads as source assets, and triggers
// partial retry of a single failed generation task without disturbing the
// products that already succeeded.
package workspace

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	_ "image/gif"  // register decoders for dimension probing
	_ "image/jpeg" // .
	_ "image/png"  // .

	"gameasset/internal/store"
)

// Service backs the workspace HTTP API.
type Service struct {
	store    *store.Store
	assetDir string
	now      func() time.Time
	newID    func() string
	// retry runs a failed task again; injected to avoid importing generation.
	retry func(sessionID, taskID string) error
	// cancel aborts an in-flight (queued/running) task and deletes its record;
	// injected so workspace doesn't import generation/video. Returns rows removed.
	cancel func(sessionID, taskID string) (int64, error)
}

// NewService constructs the workspace service. retryFn re-runs a failed task
// (wired to generation.Service.Retry in main). cancelFn aborts an in-flight task
// (wired to the generation/video cancel dispatch in main).
func NewService(st *store.Store, assetDir string, newID func() string, retryFn func(sessionID, taskID string) error, cancelFn func(sessionID, taskID string) (int64, error)) *Service {
	return &Service{
		store:    st,
		assetDir: assetDir,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    newID,
		retry:    retryFn,
		cancel:   cancelFn,
	}
}

// AssetView is the workspace representation of one asset.
type AssetView struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Mime     string `json:"mime"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Provider string `json:"provider,omitempty"`
	ParentID string `json:"parentId,omitempty"`
	URL      string `json:"url"`
	// CreatedAt lets the frontend order the timeline by real creation time and
	// show relative timestamps. RFC3339; sourced from the stored asset row.
	CreatedAt string `json:"createdAt,omitempty"`
}

// TaskView is the workspace representation of one task (placeholder card).
type TaskView struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Intent   string `json:"intent,omitempty"`
	Error    string `json:"error,omitempty"`
	AssetID  string `json:"assetId,omitempty"`
}

// RegisterRoutes mounts the workspace API on the mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/session/{id}/assets", s.handleListAssets)
	mux.HandleFunc("GET /api/session/{id}/assets/{assetId}/raw", s.handleAssetRaw)
	mux.HandleFunc("DELETE /api/session/{id}/assets/{assetId}", s.handleDeleteAsset)
	mux.HandleFunc("GET /api/session/{id}/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/session/{id}/upload", s.handleUpload)
	mux.HandleFunc("POST /api/session/{id}/tasks/{taskId}/retry", s.handleRetry)
	mux.HandleFunc("DELETE /api/session/{id}/tasks/{taskId}", s.handleDeleteTask)
	mux.HandleFunc("POST /api/session/{id}/tasks/failed/clear", s.handleClearFailed)
	mux.HandleFunc("POST /api/session/{id}/clear", s.handleClear)
}

// handleDeleteAsset removes a single asset (record + file) scoped to its
// session. Returns 404 when the asset does not belong to the session.
func (s *Service) handleDeleteAsset(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	assetID := r.PathValue("assetId")
	path, err := s.store.DeleteAsset(sessionID, assetID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if path == "" {
		http.NotFound(w, r)
		return
	}
	_ = os.Remove(path) // best-effort file cleanup; row already gone
	writeJSON(w, map[string]string{"status": "deleted", "assetId": assetID})
}

// handleClear empties the session workspace: deletes all assets (records +
// files) and removes queued/running task placeholders. Scoped to the session.
func (s *Service) handleClear(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	paths, err := s.store.DeleteSessionAssets(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, p := range paths {
		_ = os.Remove(p)
	}
	if err := s.store.DeleteUnfinishedTasks(sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "cleared", "removed": len(paths)})
}

func (s *Service) handleListAssets(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	assets, err := s.store.ListAssets(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]AssetView, 0, len(assets))
	for _, a := range assets {
		out = append(out, AssetView{
			ID: a.ID, Kind: a.Kind, Mime: a.Mime, Width: a.Width, Height: a.Height,
			Provider: a.Provider, ParentID: a.ParentID,
			URL:       fmt.Sprintf("/api/session/%s/assets/%s/raw", sessionID, a.ID),
			CreatedAt: a.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, map[string]any{"assets": out})
}

// handleAssetRaw streams an asset's bytes. Session ownership is enforced by
// GetAsset (an asset id from another session yields not-found). Shared by the
// workspace preview and the download flow.
func (s *Service) handleAssetRaw(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	assetID := r.PathValue("assetId")
	asset, err := s.store.GetAsset(sessionID, assetID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if asset == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", asset.Mime)
	http.ServeFile(w, r, asset.Path)
}

func (s *Service) handleListTasks(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	tasks, err := s.store.ListTasks(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]TaskView, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, TaskView{
			ID: t.ID, Kind: t.Kind, Status: t.Status, Progress: t.Progress,
			Intent: t.Intent, Error: t.Error, AssetID: t.AssetID,
		})
	}
	writeJSON(w, map[string]any{"tasks": out})
}

// handleUpload accepts a multipart "file" field, stores it as a source asset,
// and returns the new asset view so it can seed the workspace.
func (s *Service) handleUpload(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if _, err := s.store.GetSession(sessionID); err != nil {
		http.Error(w, "unknown session", http.StatusBadRequest)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32 MiB
		http.Error(w, "bad multipart form", http.StatusBadRequest)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	cfg, format, err := image.DecodeConfig(file)
	if err != nil {
		http.Error(w, "unsupported or corrupt image", http.StatusBadRequest)
		return
	}
	if _, err := file.Seek(0, 0); err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}

	assetID := s.newID()
	dir := filepath.Join(s.assetDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	ext := filepath.Ext(hdr.Filename)
	if ext == "" {
		ext = "." + format
	}
	path := filepath.Join(dir, assetID+ext)
	dst, err := os.Create(path)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		http.Error(w, "write error", http.StatusInternalServerError)
		return
	}
	dst.Close()

	mime := "image/" + format
	rec := store.AssetRecord{
		ID: assetID, SessionID: sessionID, Kind: "upload", Path: path, Mime: mime,
		Width: cfg.Width, Height: cfg.Height, CreatedAt: s.now(),
	}
	if err := s.store.InsertAsset(rec); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, AssetView{
		ID: assetID, Kind: "upload", Mime: mime, Width: cfg.Width, Height: cfg.Height,
		URL:       fmt.Sprintf("/api/session/%s/assets/%s/raw", sessionID, assetID),
		CreatedAt: rec.CreatedAt.Format(time.RFC3339),
	})
}

// handleRetry re-runs a single failed task. Already-succeeded products are
// untouched (partial retry).
func (s *Service) handleRetry(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	taskID := r.PathValue("taskId")
	if s.retry == nil {
		http.Error(w, "retry not available", http.StatusServiceUnavailable)
		return
	}
	if err := s.retry(sessionID, taskID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]string{"status": "queued", "taskId": taskID})
}

// handleDeleteTask removes a single task. For an in-flight (queued/running)
// task it routes through cancel — aborting the underlying generation so no
// orphan product is persisted. For a terminal task (done/failed) it just
// deletes the record. 404 when the task is not found.
func (s *Service) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	taskID := r.PathValue("taskId")

	rec, err := s.store.GetTask(sessionID, taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rec == nil {
		http.NotFound(w, r)
		return
	}

	inFlight := rec.Status == "queued" || rec.Status == "running"
	if inFlight && s.cancel != nil {
		n, err := s.cancel(sessionID, taskID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if n == 0 {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]string{"status": "cancelled", "taskId": taskID})
		return
	}

	n, err := s.store.DeleteTask(sessionID, taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n == 0 {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]string{"status": "deleted", "taskId": taskID})
}

// handleClearFailed removes every failed task for the session in one shot
// (one-click cleanup of the failed milestone).
func (s *Service) handleClearFailed(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	n, err := s.store.DeleteFailedTasks(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "cleared", "removed": n})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
