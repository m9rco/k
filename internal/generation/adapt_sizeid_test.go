package generation

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"gameasset/internal/transport"
)

// readTaskQueuedSizeID subscribes to a task's SSE stream after it has reached a
// terminal state, so the broker replays the full history (including the initial
// task_queued event). It returns the sizeId carried on that event, or "" if the
// event never set one. Mirrors the SSE consumer pattern in transport_test.
func readTaskQueuedSizeID(t *testing.T, broker *transport.TaskBroker, taskID string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/tasks/"+taskID+"/events", nil)
	// The task is terminal by call time, so ServeSSE replays history and returns
	// immediately without blocking on live events.
	broker.ServeSSE(rec, req, taskID)

	for _, line := range strings.Split(rec.Body.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var ev struct {
			Type string `json:"type"`
			Data struct {
				SizeID string `json:"sizeId"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		if ev.Type == string(transport.EventTaskQueued) {
			return ev.Data.SizeID
		}
	}
	return ""
}

// TestAdaptAITaskQueuedCarriesSizeID asserts the AI-repaint path tags its
// task_queued event with the adapt sizeId. The stamp album submits over the
// agent WS and never sees the taskId, so it relies on this sizeId to map a later
// task_failed back to the right slot and offer an in-place retry.
func TestAdaptAITaskQueuedCarriesSizeID(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	// 1920×1080 → 720×1280 is an orientation flip → AI path → goes through Start.
	const sizeID = "flip.portrait.720x1280"
	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{sizeID}, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].Via != AdaptViaAI {
		t.Fatalf("expected one AI outcome, got %+v", outcomes)
	}
	taskID := outcomes[0].TaskID
	if taskID == "" {
		t.Fatal("AI path must return a task ID")
	}
	// Let the task settle so the SSE replay is complete (task_queued is the first
	// event regardless, but waiting keeps the stream non-live for a clean replay).
	if rec := waitTask(t, st, "s", taskID); rec.Status != "done" {
		t.Fatalf("AI task not done: %q", rec.Error)
	}
	if got := readTaskQueuedSizeID(t, svc.broker, taskID); got != sizeID {
		t.Errorf("task_queued sizeId = %q, want %q", got, sizeID)
	}
}

// TestRetryTaskQueuedCarriesSizeID asserts the in-place Retry path re-tags its
// fresh task_queued event with the same sizeId after Broker.Reset wipes history,
// so the album re-binds the retry stream's events to the original slot.
func TestRetryTaskQueuedCarriesSizeID(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	const sizeID = "flip.square.512x512"
	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{sizeID}, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	taskID := outcomes[0].TaskID
	if rec := waitTask(t, st, "s", taskID); rec.Status != "done" {
		t.Fatalf("setup task not done: %q", rec.Error)
	}

	// Force the task into a failed state so Retry accepts it, then retry in place.
	svc.fail(taskID, "s", "synthetic failure for retry test")
	if err := svc.Retry(context.Background(), "s", taskID); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if rec := waitTask(t, st, "s", taskID); rec.Status != "done" {
		t.Fatalf("retried task not done: %q", rec.Error)
	}
	// After Reset the stream restarts at seq 1; the retry's task_queued must again
	// carry the sizeId (else the album can't re-bind the retry to its slot).
	if got := readTaskQueuedSizeID(t, svc.broker, taskID); got != sizeID {
		t.Errorf("retry task_queued sizeId = %q, want %q", got, sizeID)
	}
}
