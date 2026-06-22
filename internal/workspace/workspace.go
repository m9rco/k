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
	// retryAsset re-runs the AI flow that produced a SUCCEEDED product, yielding a
	// new task whose product is a new asset (the original is left in place).
	// Injected (wired to generation.Service.RetryAsset) so workspace doesn't import
	// generation; nil disables the asset-retry endpoint. Returns the new task id.
	retryAsset func(sessionID, assetID string) (string, error)
	// prewarm fires the upload-time vision analysis (publish → analyze → cache by
	// md5) in the background after a successful upload. Injected so workspace
	// doesn't import cos/vision; nil disables prewarming (graceful no-op).
	prewarm func(sessionID, assetID, path, mime string)
	// describeRegion resolves a user selection on an asset into a structured
	// feature description (+ optional bounding box). Three modes share one fn:
	//   - point mode (px,py ≥ 0): the vision model looks at the FULL image and the
	//     click point, identifies the object under it, and returns its box + desc.
	//   - polygon mode (len(poly) ≥ 3): the server masks the lasso shape to
	//     transparent outside, crops the bbox, and describes that cutout. The
	//     returned box is the polygon's bounding box.
	//   - rect mode (px,py < 0, no poly): crops the [x,y,w,h] box and describes
	//     the crop (box returned is the input rect).
	// Injected in main (vision.RegionLocator for point, crop.RegionBytes/
	// RegionPolygonBytes + Analyzer.DescribeRegion otherwise) so workspace doesn't
	// import crop/vision; nil disables the endpoint (returns 503). Returns
	// (description, box, error) where box is normalized [0,1].
	describeRegion func(sessionID, assetID string, x, y, w, h, px, py float64, poly [][2]float64) (desc string, bx, by, bw, bh float64, err error)
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

// SetRetryAsset wires the successful-product retry (generation.Service.RetryAsset).
// Leaving it unset makes the asset-retry endpoint return 503.
func (s *Service) SetRetryAsset(fn func(sessionID, assetID string) (string, error)) {
	s.retryAsset = fn
}

// SetPrewarm wires the upload-time vision analysis prewarm. Leaving it unset
// disables prewarming (uploads still succeed; later adapts analyze on demand).
func (s *Service) SetPrewarm(fn func(sessionID, assetID, path, mime string)) {
	s.prewarm = fn
}

// SetDescribeRegion wires the region-description capability. Leaving it unset
// makes the describe-region endpoint return 503. See the field doc for the
// point/polygon/rect calling convention (px,py < 0 and empty poly means rect).
func (s *Service) SetDescribeRegion(fn func(sessionID, assetID string, x, y, w, h, px, py float64, poly [][2]float64) (string, float64, float64, float64, float64, error)) {
	s.describeRegion = fn
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
	// SizeID is set for platform-adaptation products (crop or AI repaint). It
	// lets the frontend collapse a batch of adapted sizes into one timeline node.
	SizeID string `json:"sizeId,omitempty"`
	// Retryable is true when this product carries a generation origin (an AI flow
	// that can be re-run). Uploads and deterministic crops have none → false, so
	// the frontend shows no retry affordance for them.
	Retryable bool `json:"retryable,omitempty"`
	// ReferenceIDs lists the reference asset ids used to produce this asset
	// (derived from gen_origin). Populated only for AI products with ≥2 references.
	ReferenceIDs []string `json:"referenceIds,omitempty"`
	URL          string   `json:"url"`
	CreatedAt    string   `json:"createdAt,omitempty"`
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
	mux.HandleFunc("POST /api/session/{id}/assets/{assetId}/describe-region", s.handleDescribeRegion)
	mux.HandleFunc("DELETE /api/session/{id}/assets/{assetId}", s.handleDeleteAsset)
	mux.HandleFunc("POST /api/session/{id}/assets/{assetId}/retry", s.handleRetryAsset)
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
// files) and all task rows (in-flight placeholders AND done/failed history), so
// the session returns to a clean slate and the frontend lands back on the home
// screen. Scoped to the session.
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
	if err := s.store.DeleteSessionTasks(sessionID); err != nil {
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
		view := AssetView{
			ID: a.ID, Kind: a.Kind, Mime: a.Mime, Width: a.Width, Height: a.Height,
			Provider: a.Provider, ParentID: a.ParentID,
			Retryable: a.GenOrigin != "",
			URL:       fmt.Sprintf("/api/session/%s/assets/%s/raw", sessionID, a.ID),
			CreatedAt: a.CreatedAt.Format(time.RFC3339),
		}
		if a.Meta != "" {
			var m struct {
				SizeID string `json:"sizeId"`
			}
			if json.Unmarshal([]byte(a.Meta), &m) == nil {
				view.SizeID = m.SizeID
			}
		}
		if a.GenOrigin != "" {
			var g struct {
				ReferenceAssetIDs []string `json:"reference_asset_ids"`
			}
			if json.Unmarshal([]byte(a.GenOrigin), &g) == nil && len(g.ReferenceAssetIDs) >= 2 {
				view.ReferenceIDs = g.ReferenceAssetIDs
			}
		}
		out = append(out, view)
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

// regionRequest is the body of POST .../describe-region. Three modes:
//   - point: send Px,Py (normalized click ∈ [0,1]); X/Y/W/H omitted/ignored. The
//     vision model looks at the full image and returns the clicked object's box.
//   - polygon: send Points ([{x,y},…] normalized, ≥3); the server crops the
//     polygon's bbox and masks pixels outside the lasso to transparent, then
//     describes that cutout. Takes precedence over rect when present.
//   - rect: send X,Y,W,H (normalized box); Px,Py/Points omitted. Crop+describe.
//
// Px/Py default to a sentinel (< 0) when absent so the handler can tell which
// mode the client intends.
type regionPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type regionRequest struct {
	X      float64       `json:"x"`
	Y      float64       `json:"y"`
	W      float64       `json:"w"`
	H      float64       `json:"h"`
	Px     *float64      `json:"px"`
	Py     *float64      `json:"py"`
	Points []regionPoint `json:"points"`
}

// handleDescribeRegion resolves the user's selection into a structured feature
// description. POINT mode (px,py present) lets the vision model inspect the full
// image and the click point. POLYGON mode (points ≥3) masks the lassoed shape to
// transparent outside and describes the cutout. RECT mode (x,y,w,h) crops the box
// and describes the crop. The instruction is fixed server-side (no user text).
// Returns 503 when unwired, 400 on a bad selection, and a graceful
// {available:false} body when the vision model is down so the frontend degrades
// to plain-text editing.
func (s *Service) handleDescribeRegion(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	assetID := r.PathValue("assetId")
	if s.describeRegion == nil {
		http.Error(w, "region description not available", http.StatusServiceUnavailable)
		return
	}
	var req regionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	// Mode selection: polygon (≥3 pts) > point > rect.
	point := req.Px != nil && req.Py != nil
	poly := make([][2]float64, 0, len(req.Points))
	var px, py float64
	if len(req.Points) >= 3 {
		// Polygon mode: validate every vertex is in range.
		for _, p := range req.Points {
			if p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
				http.Error(w, "invalid polygon point", http.StatusBadRequest)
				return
			}
			poly = append(poly, [2]float64{p.X, p.Y})
		}
		px, py = -1, -1
	} else if point {
		px, py = *req.Px, *req.Py
		if px < 0 || px > 1 || py < 0 || py > 1 {
			http.Error(w, "invalid click point", http.StatusBadRequest)
			return
		}
	} else {
		// Sentinel: negative px,py + empty poly tells the wired fn this is rect.
		px, py = -1, -1
		if req.W <= 0 || req.H <= 0 || req.X < 0 || req.Y < 0 ||
			req.X > 1 || req.Y > 1 || req.X+req.W > 1.0001 || req.Y+req.H > 1.0001 {
			http.Error(w, "invalid region box", http.StatusBadRequest)
			return
		}
	}
	desc, bx, by, bw, bh, err := s.describeRegion(sessionID, assetID, req.X, req.Y, req.W, req.H, px, py, poly)
	if err != nil {
		// Graceful degradation: the frontend falls back to plain-text editing.
		writeJSON(w, map[string]any{"available": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{
		"available":   true,
		"description": desc,
		"box":         map[string]float64{"x": bx, "y": by, "w": bw, "h": bh},
	})
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
	// Upload-time vision prewarm: fire-and-forget publish → analyze → cache by md5
	// so a later adapt of this image hits the cache instead of re-analyzing. Best
	// effort — must not block or fail the upload response. Disabled (nil) when
	// COS/vision are unconfigured.
	if s.prewarm != nil {
		s.prewarm(sessionID, assetID, path, mime)
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

// handleRetryAsset re-runs the AI flow that produced a SUCCEEDED product. Unlike
// handleRetry (which re-runs a failed task in place), this yields a NEW task whose
// product is a new asset; the original is left untouched. Returns 503 when the
// retry capability is unwired, and 400 when the asset carries no generation origin
// (uploads / deterministic crops are not retryable).
func (s *Service) handleRetryAsset(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	assetID := r.PathValue("assetId")
	if s.retryAsset == nil {
		http.Error(w, "retry not available", http.StatusServiceUnavailable)
		return
	}
	taskID, err := s.retryAsset(sessionID, assetID)
	if err != nil {
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
