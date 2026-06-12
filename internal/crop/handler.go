package crop

import (
	"encoding/json"
	"net/http"
)

// RegisterRoutes mounts the crop-related HTTP API. The crop action itself is
// driven through the agent/workspace; this endpoint only serves the channel
// catalog that the frontend renders as a layered (channel → asset type → size)
// selector.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/platforms", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"channels": s.Channels()})
	})
}
