package layering

import (
	"encoding/json"
	"net/http"

	applog "gameasset/internal/log"
)

// RegisterRoutes mounts the layer-split endpoint. The split is synchronous
// (vision detection + segmentation-mask cutouts of the original pixels, no
// generation), so it runs here and returns the produced layers — each with its
// normalized position box — in one response; the frontend opens the compositing
// canvas with them.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/session/{id}/layer-split", s.handleSplit)
}

type splitRequest struct {
	SourceAssetID string `json:"sourceAssetId"`
}

func (s *Service) handleSplit(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	lg := applog.From(r.Context())
	var req splitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	if req.SourceAssetID == "" {
		http.Error(w, "sourceAssetId is required", http.StatusBadRequest)
		return
	}
	lg.Info().Str("event", "layer.split.request").Str("session", sessionID).Str("source", req.SourceAssetID).Msg("收到图层精修请求")
	res, err := s.Split(r.Context(), sessionID, req.SourceAssetID)
	if err != nil {
		lg.Error().Str("event", "layer.split.response_error").Err(err).Msg("图层精修返回错误")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}
