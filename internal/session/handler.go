package session

import (
	"encoding/json"
	"net/http"
)

// createRequest is the body of POST /api/session.
type createRequest struct {
	Fingerprint Fingerprint `json:"fingerprint"`
	// SessionID, when present (from sessionStorage), is tried for reconnect first.
	SessionID string `json:"sessionId"`
}

// createResponse is returned by POST /api/session.
type createResponse struct {
	SessionID string `json:"sessionId"`
	Created   bool   `json:"created"`
	Reused    bool   `json:"reused"`
}

// RegisterRoutes mounts the session HTTP API on the given mux.
func (m *Manager) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/session", m.handleCreate)
	mux.HandleFunc("GET /api/session/{id}/context", m.handleContext)
}

// handleCreate creates a new session or reconnects to an existing one.
//
// Reconnect precedence: an explicit sessionId (from sessionStorage) is tried
// first; on miss, a session id is derived from the browser fingerprint.
func (m *Manager) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.SessionID != "" {
		reused, err := m.Reconnect(req.SessionID)
		if err != nil {
			http.Error(w, "session reconnect failed", http.StatusInternalServerError)
			return
		}
		if reused {
			writeJSON(w, createResponse{SessionID: req.SessionID, Reused: true})
			return
		}
	}

	id, created, err := m.EnsureID(req.Fingerprint)
	if err != nil {
		http.Error(w, "session create failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, createResponse{SessionID: id, Created: created, Reused: !created})
}

// handleContext returns the context status for a session.
func (m *Manager) handleContext(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	status, err := m.Status(id)
	if err != nil {
		http.Error(w, "status failed", http.StatusInternalServerError)
		return
	}
	if !status.Active {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSON(w, status)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
