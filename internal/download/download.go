// Package download serves asset files for download: single assets as file
// attachments, and multi-select batches packaged server-side into a single zip.
// Invalid or not-yet-ready selections are skipped, and the response reports
// which ids were omitted so the frontend can surface a notice.
package download

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"gameasset/internal/store"
)

// Service backs the download/packaging HTTP API.
type Service struct {
	store *store.Store
}

// NewService constructs a download service.
func NewService(st *store.Store) *Service {
	return &Service{store: st}
}

// RegisterRoutes mounts the download endpoints on the mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/session/{id}/assets/{assetId}/download", s.handleSingle)
	mux.HandleFunc("POST /api/session/{id}/download/zip", s.handleZip)
}

// handleSingle returns one asset as a file attachment.
func (s *Service) handleSingle(w http.ResponseWriter, r *http.Request) {
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
	name := assetID + extForMime(asset.Mime)
	w.Header().Set("Content-Type", asset.Mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeFile(w, r, asset.Path)
}

// zipRequest is the body of a batch-download request.
type zipRequest struct {
	AssetIDs []string `json:"assetIds"`
}

// handleZip packages the selected assets into a single zip. Selections that are
// unknown, belong to another session, or whose file is missing are skipped; the
// skipped ids are returned in a trailer header so the client can notify the user.
//
// The zip is streamed directly to the response. Because a streaming zip cannot
// change status mid-write, an empty valid selection is reported as 400 before
// any bytes are written.
func (s *Service) handleZip(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	var req zipRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}

	// Resolve selections into valid files first so we can reject an all-invalid
	// batch before streaming and report skipped ids deterministically.
	type entry struct{ id, path, mime, kind, meta string }
	var valid []entry
	var skipped []string
	for _, aid := range req.AssetIDs {
		asset, err := s.store.GetAsset(sessionID, aid)
		if err != nil || asset == nil {
			skipped = append(skipped, aid)
			continue
		}
		if _, err := os.Stat(asset.Path); err != nil {
			skipped = append(skipped, aid)
			continue
		}
		valid = append(valid, entry{id: aid, path: asset.Path, mime: asset.Mime, kind: asset.Kind, meta: asset.Meta})
	}

	if len(valid) == 0 {
		http.Error(w, "no valid assets to package", http.StatusBadRequest)
		return
	}

	// Surface skipped ids in a header (readable before the body for fetch()).
	if len(skipped) > 0 {
		w.Header().Set("X-Skipped-Assets", strings.Join(skipped, ","))
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "assets-"+stamp+".zip"))

	zw := zip.NewWriter(w)
	defer zw.Close()
	// Per-directory counters keep names unique within each channel/size folder.
	seen := map[string]int{}
	for _, e := range valid {
		dir := zipDir(e.kind, e.meta)
		seen[dir]++
		name := fmt.Sprintf("%s/%02d-%s%s", dir, seen[dir], e.id, extForMime(e.mime))
		if err := addToZip(zw, name, e.path); err != nil {
			// Mid-stream failure: stop adding; the partial zip still has prior files.
			return
		}
	}
}

// zipDir derives the in-zip directory for an asset. Cropped products carry a
// CropMeta JSON ({channelId,sizeId}) and are filed under channel/size; anything
// without that metadata (uploads, raw generations) falls back to a kind bucket.
func zipDir(kind, meta string) string {
	if meta != "" {
		var m struct {
			ChannelID string `json:"channelId"`
			SizeID    string `json:"sizeId"`
		}
		if json.Unmarshal([]byte(meta), &m) == nil && m.ChannelID != "" {
			seg := m.SizeID
			if seg == "" {
				seg = "misc"
			}
			return sanitizeSeg(m.ChannelID) + "/" + sanitizeSeg(seg)
		}
	}
	switch kind {
	case "upload":
		return "uploads"
	case "generated":
		return "generated"
	case "cropped":
		return "cropped"
	default:
		return "misc"
	}
}

// sanitizeSeg keeps a path segment safe for a zip entry: no separators or
// traversal, trimmed to a sane length.
func sanitizeSeg(s string) string {
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "\\", "-")
	s = strings.ReplaceAll(s, "..", "-")
	s = strings.TrimSpace(s)
	if s == "" {
		return "misc"
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// addToZip copies one file into the zip under name.
func addToZip(zw *zip.Writer, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hw, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(hw, f)
	return err
}

// extForMime maps a content type to a file extension for download filenames.
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
		if i := strings.LastIndex(mime, "/"); i >= 0 {
			return "." + mime[i+1:]
		}
		return ".bin"
	}
}
