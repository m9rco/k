package crop

import (
	"encoding/json"
	"net/http"
)

// RegisterRoutes mounts the crop-related HTTP API: the channel catalog the
// frontend renders as a layered selector, and a direct crop endpoint. Cropping
// is pure image processing (no AI), so the frontend calls it directly rather
// than routing through the conversational agent — avoiding LLM latency and rate
// limits for what is a deterministic operation. The agent's crop_to_sizes tool
// remains for conversational ("把这张图裁成…") requests.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/platforms", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"channels": s.Channels()})
	})
	mux.HandleFunc("POST /api/session/{id}/crop", s.handleCrop)
}

// cropRequest is the body of POST /api/session/{id}/crop.
type cropRequest struct {
	// SourceAssetID is the workspace asset to crop.
	SourceAssetID string `json:"sourceAssetId"`
	// SizeIDs are the globally-unique size ids to produce (may span channels).
	SizeIDs []string `json:"sizeIds"`
	// Lossless toggles PNG lossless optimization of products (default true when omitted).
	Lossless *bool `json:"lossless,omitempty"`
}

// handleCrop performs a direct, synchronous crop and returns the produced
// assets (with their dimensions and channel/size attribution) so the frontend
// can decide whether to surface them in the workspace or package them for
// download. Session ownership is enforced by CropToSizes via the store.
func (s *Service) handleCrop(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	var req cropRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	if req.SourceAssetID == "" || len(req.SizeIDs) == 0 {
		http.Error(w, "sourceAssetId and sizeIds are required", http.StatusBadRequest)
		return
	}
	lossless := req.Lossless == nil || *req.Lossless
	results, err := s.CropToSizes(sessionID, req.SourceAssetID, req.SizeIDs, lossless)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
}
