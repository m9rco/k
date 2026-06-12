package transport

import "net/http"

// RegisterRoutes mounts the WebSocket and SSE endpoints on the mux.
func RegisterRoutes(mux *http.ServeMux, hub *Hub, broker *TaskBroker) {
	mux.HandleFunc("GET /api/ws", hub.ServeWS)
	mux.HandleFunc("GET /api/tasks/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		broker.ServeSSE(w, r, r.PathValue("id"))
	})
}
