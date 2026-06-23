package composite

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// RegisterRoutes mounts the composite persist endpoint. Like the crop endpoint,
// this is a direct, deterministic operation (no AI, no LLM): the browser flattens
// the compositing canvas to a PNG and posts the bytes here to land them in the
// workspace. Routing through the conversational agent would add latency and rate
// limits for what is pure storage.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/session/{id}/composite", s.handlePersist)
}

// handlePersist reads the flattened composite image from the request body and
// persists it as a session asset. The raw image bytes ARE the body (so the large
// PNG is streamed, not base64-bloated); optional source layer ids come from the
// `sourceAssetIds` query param (comma-separated) for derivation labelling, and
// `lossless=0` disables PNG optimization. Session ownership is enforced by the
// store via the path id.
func (s *Service) handlePersist(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	data, err := io.ReadAll(io.LimitReader(r.Body, maxCompositeBytes+1))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var sources []string
	if raw := strings.TrimSpace(r.URL.Query().Get("sourceAssetIds")); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				sources = append(sources, p)
			}
		}
	}
	lossless := r.URL.Query().Get("lossless") != "0"
	res, err := s.Persist(sessionID, data, sources, lossless)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}
