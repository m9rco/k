// Package transport provides the real-time channels for the asset studio:
//
//   - WebSocket carries the bidirectional conversation channel: user messages,
//     streamed agent replies, tool-call events, and capsule selections.
//   - SSE (HTTP streaming) carries one-way progress for long-running tasks
//     (image generation / cropping), with Last-Event-ID resume so a reconnecting
//     client recovers the latest state without losing the final result.
//
// All events share a single envelope so the frontend can dispatch uniformly.
package transport

import "time"

// EventType enumerates the kinds of real-time events.
type EventType string

const (
	// Conversation / WebSocket events.
	EventMessage    EventType = "message"     // agent reply increment
	EventToolCall   EventType = "tool_call"   // a tool invocation started/updated
	EventToolResult EventType = "tool_result" // a tool produced a result
	EventCapsule    EventType = "capsule"     // server asks user to pick (e.g. sizes)
	EventError      EventType = "error"       // recoverable error notice (toast)

	// Task / SSE events.
	EventTaskQueued   EventType = "task_queued"
	EventTaskRunning  EventType = "task_running"
	EventTaskProgress EventType = "task_progress"
	EventTaskDone     EventType = "task_done"
	EventTaskFailed   EventType = "task_failed"
)

// Event is the unified envelope sent over both WS and SSE.
type Event struct {
	// Seq is a monotonically increasing id within a stream, used as the SSE
	// event id for Last-Event-ID resume.
	Seq int `json:"seq"`
	// Type discriminates the payload.
	Type EventType `json:"type"`
	// SessionID scopes the event to a session.
	SessionID string `json:"sessionId,omitempty"`
	// TaskID is set for task-progress events.
	TaskID string `json:"taskId,omitempty"`
	// Data carries the type-specific payload (already JSON-serializable).
	Data any `json:"data,omitempty"`
	// At is the event timestamp.
	At time.Time `json:"at"`
}

// IsTerminal reports whether the event ends a task stream.
func (e Event) IsTerminal() bool {
	return e.Type == EventTaskDone || e.Type == EventTaskFailed
}
