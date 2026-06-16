package transport

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// taskStream holds the buffered event history and live subscribers for one task.
type taskStream struct {
	mu        sync.Mutex
	events    []Event // full history (small: a handful of progress ticks)
	terminal  bool
	subs      map[chan Event]struct{}
	updatedAt time.Time
}

// TaskBroker manages SSE task-progress streams. It retains each task's event
// history so a client reconnecting with Last-Event-ID can replay missed events
// and always observe the terminal (done/failed) result.
type TaskBroker struct {
	mu      sync.Mutex
	streams map[string]*taskStream
	now     func() time.Time
}

// NewTaskBroker constructs an empty broker.
func NewTaskBroker() *TaskBroker {
	return &TaskBroker{
		streams: make(map[string]*taskStream),
		now:     func() time.Time { return time.Now().UTC() },
	}
}

func (b *TaskBroker) stream(taskID string) *taskStream {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.streams[taskID]
	if !ok {
		s = &taskStream{subs: make(map[chan Event]struct{})}
		b.streams[taskID] = s
	}
	return s
}

// Publish appends an event to a task stream and fans it out to live subscribers.
// The Seq and At fields are assigned here so callers need not track them.
func (b *TaskBroker) Publish(taskID string, typ EventType, sessionID string, data any) Event {
	s := b.stream(taskID)
	s.mu.Lock()
	defer s.mu.Unlock()

	ev := Event{
		Seq:       len(s.events) + 1,
		Type:      typ,
		SessionID: sessionID,
		TaskID:    taskID,
		Data:      data,
		At:        b.now(),
	}
	s.events = append(s.events, ev)
	s.updatedAt = ev.At
	if ev.IsTerminal() {
		s.terminal = true
	}
	for ch := range s.subs {
		// Non-blocking send: subscribers have buffered channels; drop on overflow
		// since the full history is replayable on reconnect.
		select {
		case ch <- ev:
		default:
		}
	}
	return ev
}

// subscribe registers a subscriber, returning the channel, a replay slice of
// events after afterSeq, and whether the stream is already terminal.
func (b *TaskBroker) subscribe(taskID string, afterSeq int) (chan Event, []Event, bool) {
	s := b.stream(taskID)
	s.mu.Lock()
	defer s.mu.Unlock()

	var replay []Event
	for _, ev := range s.events {
		if ev.Seq > afterSeq {
			replay = append(replay, ev)
		}
	}
	if s.terminal {
		// No live updates will follow; caller just sends replay and closes.
		return nil, replay, true
	}
	ch := make(chan Event, 16)
	s.subs[ch] = struct{}{}
	return ch, replay, false
}

func (b *TaskBroker) unsubscribe(taskID string, ch chan Event) {
	s := b.stream(taskID)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subs, ch)
}

// Reset clears a task stream's event history and terminal flag so the same task
// id can be re-run (a retry) on a fresh stream. Without this the stream stays
// terminal: a new subscriber would replay the stale task_failed event and never
// register for the retry's live events, freezing the UI on the old failure.
// Live subscribers are preserved (a client that re-subscribed before Reset keeps
// receiving), and Seq restarts at 1 so reconnecting clients replay only the new
// attempt. Safe to call on an unknown task (creates an empty stream).
func (b *TaskBroker) Reset(taskID string) {
	s := b.stream(taskID)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = nil
	s.terminal = false
	s.updatedAt = b.now()
}

// ServeSSE handles GET /api/tasks/{id}/events. It replays history after the
// client's Last-Event-ID, then streams live events until the task is terminal
// or the client disconnects.
func (b *TaskBroker) ServeSSE(w http.ResponseWriter, r *http.Request, taskID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Headers that defeat proxy buffering and keep the stream open.
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // disable nginx proxy buffering

	afterSeq := parseLastEventID(r.Header.Get("Last-Event-ID"))

	ch, replay, terminal := b.subscribe(taskID, afterSeq)
	for _, ev := range replay {
		writeSSE(w, ev)
	}
	flusher.Flush()
	if terminal {
		return
	}
	defer b.unsubscribe(taskID, ch)

	ctx := r.Context()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			writeSSE(w, ev)
			flusher.Flush()
			if ev.IsTerminal() {
				return
			}
		case <-keepalive.C:
			// SSE comment line keeps intermediaries from closing idle connections.
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// PublishWithContext is a convenience that respects cancellation while keeping
// the synchronous Publish semantics (used by task runners).
func (b *TaskBroker) PublishWithContext(ctx context.Context, taskID string, typ EventType, sessionID string, data any) {
	if ctx.Err() != nil {
		return
	}
	b.Publish(taskID, typ, sessionID, data)
}
