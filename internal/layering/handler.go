package layering

import (
	"encoding/json"
	"net/http"
)

// RegisterRoutes mounts the layer-split endpoint. The split itself drives the
// generation pipeline (vision detection + Gemini cutouts + background inpaint),
// so it runs synchronously here and returns the produced layers in one response;
// the frontend opens the compositing canvas with them.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/session/{id}/layer-split", s.handleSplit)
}

type splitRequest struct {
	SourceAssetID string `json:"sourceAssetId"`
}

func (s *Service) handleSplit(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	var req splitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	if req.SourceAssetID == "" {
		http.Error(w, "sourceAssetId is required", http.StatusBadRequest)
		return
	}
	res, err := s.Split(r.Context(), sessionID, req.SourceAssetID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}
