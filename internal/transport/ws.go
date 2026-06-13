package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Inbound is a client→server message received over the WebSocket.
type Inbound struct {
	// Type is "user_message" or "capsule_select".
	Type string `json:"type"`
	// Text carries a free-form user message.
	Text string `json:"text,omitempty"`
	// Selection carries the chosen value(s) for a capsule prompt.
	Selection []string `json:"selection,omitempty"`
	// Ref points at an asset id the message acts on (e.g. re-adjust an image).
	Ref string `json:"ref,omitempty"`
	// Refs lists multiple reference asset ids for multi-reference generation
	// (up to 6). When set, surfaced to the agent so it can pass them as
	// reference_asset_ids.
	Refs []string `json:"refs,omitempty"`
	// Lossless toggles program-side PNG lossless optimization of image products.
	// Pointer so an omitted field defaults to enabled while an explicit false
	// disables it.
	Lossless *bool `json:"lossless,omitempty"`
}

// InboundHandler processes a client message for a session. Implementations
// (the orchestration layer) drive the agent and push replies via Hub.Send.
type InboundHandler func(ctx context.Context, sessionID string, msg Inbound)

// conn wraps one live WebSocket connection.
type conn struct {
	ws  *websocket.Conn
	out chan Event
}

// Hub tracks WebSocket connections per session and fans events out to them.
type Hub struct {
	mu      sync.RWMutex
	conns   map[string]map[*conn]struct{} // sessionID -> connections
	handler InboundHandler
}

// NewHub constructs a hub with the given inbound message handler. The handler
// may be nil (messages are then ignored), which is useful in tests.
func NewHub(handler InboundHandler) *Hub {
	return &Hub{
		conns:   make(map[string]map[*conn]struct{}),
		handler: handler,
	}
}

// SetHandler installs/replaces the inbound handler (lets main wire the agent
// after the hub is constructed, avoiding an initialization cycle).
func (h *Hub) SetHandler(handler InboundHandler) {
	h.mu.Lock()
	h.handler = handler
	h.mu.Unlock()
}

// Send pushes an event to all connections of a session. It is non-blocking per
// connection; slow connections drop intermediate events rather than stall.
func (h *Hub) Send(sessionID string, ev Event) {
	ev.SessionID = sessionID
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.conns[sessionID] {
		select {
		case c.out <- ev:
		default:
		}
	}
}

func (h *Hub) add(sessionID string, c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[sessionID] == nil {
		h.conns[sessionID] = make(map[*conn]struct{})
	}
	h.conns[sessionID][c] = struct{}{}
}

func (h *Hub) remove(sessionID string, c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set := h.conns[sessionID]; set != nil {
		delete(set, c)
		if len(set) == 0 {
			delete(h.conns, sessionID)
		}
	}
}

func (h *Hub) currentHandler() InboundHandler {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.handler
}

// ServeWS handles GET /api/ws?session={id}. It upgrades the connection, then
// runs concurrent read (inbound→handler) and write (outbound events) pumps.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		http.Error(w, "missing session", http.StatusBadRequest)
		return
	}

	// No auth/origin check: this is an internal-only tool (see project.md).
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer ws.CloseNow()

	c := &conn{ws: ws, out: make(chan Event, 32)}
	h.add(sessionID, c)
	defer h.remove(sessionID, c)

	ctx := r.Context()
	go h.writePump(ctx, c)
	h.readPump(ctx, sessionID, c)
}

// writePump serializes outbound events to the connection.
func (h *Hub) writePump(ctx context.Context, c *conn) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-c.out:
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = c.ws.Write(writeCtx, websocket.MessageText, data)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// readPump reads inbound messages and dispatches them to the handler.
func (h *Hub) readPump(ctx context.Context, sessionID string, c *conn) {
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		var msg Inbound
		if err := json.Unmarshal(data, &msg); err != nil {
			h.Send(sessionID, Event{Type: EventError, Data: map[string]string{"message": "malformed message"}})
			continue
		}
		if handler := h.currentHandler(); handler != nil {
			handler(ctx, sessionID, msg)
		}
	}
}

// ConnCount returns the number of live connections for a session (for tests).
func (h *Hub) ConnCount(sessionID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns[sessionID])
}
