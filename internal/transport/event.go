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
	EventReasoning  EventType = "reasoning"   // model thinking increment (opportunistic)
	EventToolCall   EventType = "tool_call"   // a tool invocation started/updated
	EventToolResult EventType = "tool_result" // a tool finished (success or error)
	EventCapsule    EventType = "capsule"     // server asks user to pick (e.g. sizes)
	EventError      EventType = "error"       // recoverable error notice (toast)

	// EventTurnStart is emitted the instant a user message is accepted, before
	// the model is called, so the frontend can enter a loading state without
	// waiting for the first model increment (which can lag by seconds).
	//
	// Its Data carries a streaming-capability hint so the frontend can pick the
	// right wait-state tier (P1 lightweight micro-hint vs P2 static fallback):
	//   - {"streaming": true}                     normal streaming turn (default)
	//   - {"streaming": false, "degraded": true}  the turn degraded to a one-shot
	//     (re-chunked) response; the frontend should switch to the static fallback
	//     deterministically rather than waiting on its timeout.
	// The degraded variant may be emitted a second time within the same turn
	// (after the initial streaming:true), so clients MUST treat a repeat
	// turn_start idempotently (do not reset turn state, only adjust the wait tier).
	// Unknown fields are ignored by older clients (additive evolution).
	EventTurnStart EventType = "turn_start"
	// EventTurnEnd is emitted when a turn finishes (success, error, or a clarify
	// capsule was produced). Its payload carries turn-end metadata (toolUsed,
	// hasCapsule) and the latest context window state so the frontend can close
	// the loading state and refresh the context indicator.
	EventTurnEnd EventType = "turn_end"

	// EventTurnReset asks the frontend to discard the CURRENT in-flight reply and
	// reasoning increments of this turn and return to the wait (loading) state,
	// because the turn is about to re-produce them from a clean slate. It is
	// emitted before a self-correcting retry (the model only faked execution in
	// prose without a tool call): without it, the discarded fake-ack text would
	// stay accumulated on the frontend and the retry's output would append to it,
	// surfacing as duplicated confirmation text.
	//
	// Unlike turn_end it does NOT end the turn — fresh increments follow. It is
	// additive: clients that do not recognize it ignore it (per "unknown event
	// types must not error"), degrading only to the prior duplicate behavior.
	EventTurnReset EventType = "turn_reset"

	// EventFollowUp is emitted after a turn that produced workspace assets, as a
	// proactive suggestion for the user's next action.
	EventFollowUp EventType = "follow_up"

	// EventTaskCreated is broadcast over the conversation (WS) channel the moment
	// a long task is created, so the workspace can show an immediate placeholder
	// and subscribe to its SSE progress without waiting for the agent turn to end.
	EventTaskCreated EventType = "task_created"

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
