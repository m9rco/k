package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestBrokerPublishAssignsSeq(t *testing.T) {
	b := NewTaskBroker()
	e1 := b.Publish("t1", EventTaskQueued, "s", nil)
	e2 := b.Publish("t1", EventTaskRunning, "s", nil)
	if e1.Seq != 1 || e2.Seq != 2 {
		t.Fatalf("seq not monotonic: %d, %d", e1.Seq, e2.Seq)
	}
}

func TestBrokerReplayAfterLastEventID(t *testing.T) {
	b := NewTaskBroker()
	b.Publish("t1", EventTaskQueued, "s", nil)   // seq 1
	b.Publish("t1", EventTaskProgress, "s", nil) // seq 2
	b.Publish("t1", EventTaskProgress, "s", nil) // seq 3

	// Subscribe as if reconnecting after seq 1: should replay 2 and 3.
	_, replay, terminal := b.subscribe("t1", 1)
	if terminal {
		t.Fatal("stream should not be terminal yet")
	}
	if len(replay) != 2 || replay[0].Seq != 2 || replay[1].Seq != 3 {
		t.Fatalf("unexpected replay: %+v", replay)
	}
}

func TestBrokerTerminalReplayNoLiveChannel(t *testing.T) {
	b := NewTaskBroker()
	b.Publish("t1", EventTaskQueued, "s", nil)
	b.Publish("t1", EventTaskDone, "s", map[string]string{"assetId": "a1"})

	ch, replay, terminal := b.subscribe("t1", 0)
	if !terminal {
		t.Fatal("expected terminal stream")
	}
	if ch != nil {
		t.Fatal("terminal stream should not return a live channel")
	}
	// Reconnect must still observe the final result.
	last := replay[len(replay)-1]
	if last.Type != EventTaskDone {
		t.Fatalf("final event lost on reconnect: %+v", last)
	}
}

// TestBrokerResetReopensTerminalStream verifies a retry (same task id) gets a
// fresh stream: after Reset, a new subscriber is live (not terminal), the stale
// failure is gone from replay, and the retry's events are delivered. Without
// Reset the stream stays terminal and the retry's progress never reaches clients.
func TestBrokerResetReopensTerminalStream(t *testing.T) {
	b := NewTaskBroker()
	b.Publish("t1", EventTaskQueued, "s", nil)
	b.Publish("t1", EventTaskFailed, "s", map[string]string{"error": "boom"})

	// Sanity: the stream is terminal before reset.
	if _, _, terminal := b.subscribe("t1", 0); !terminal {
		t.Fatal("expected terminal before reset")
	}

	// Retry: reset, then re-run publishes fresh events.
	b.Reset("t1")
	ch, replay, terminal := b.subscribe("t1", 0)
	if terminal {
		t.Fatal("stream still terminal after reset — retry events would be dropped")
	}
	if ch == nil {
		t.Fatal("no live channel after reset — retry would not stream")
	}
	for _, ev := range replay {
		if ev.Type == EventTaskFailed {
			t.Fatalf("stale failure replayed after reset: %+v", ev)
		}
	}
	// Seq restarts at 1 so reconnecting clients only see the new attempt.
	ev := b.Publish("t1", EventTaskQueued, "s", map[string]string{"retry": "true"})
	if ev.Seq != 1 {
		t.Fatalf("seq after reset = %d, want 1", ev.Seq)
	}
	select {
	case got := <-ch:
		if got.Type != EventTaskQueued {
			t.Fatalf("unexpected live event after reset: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("retry event not delivered to live subscriber")
	}
}

func TestBrokerLiveDelivery(t *testing.T) {
	b := NewTaskBroker()
	b.Publish("t1", EventTaskQueued, "s", nil)
	ch, _, terminal := b.subscribe("t1", 1)
	if terminal {
		t.Fatal("not terminal")
	}
	b.Publish("t1", EventTaskProgress, "s", map[string]int{"pct": 50})
	select {
	case ev := <-ch:
		if ev.Type != EventTaskProgress {
			t.Fatalf("unexpected live event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive live event")
	}
}

func TestServeSSEStreamsAndResumes(t *testing.T) {
	b := NewTaskBroker()
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewHub(nil), b)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Pre-populate one event, then stream live ones.
	b.Publish("task42", EventTaskQueued, "s", nil)

	// Start SSE consumer from the beginning.
	req, _ := http.NewRequest("GET", srv.URL+"/api/tasks/task42/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Error("missing no-buffer header")
	}

	// Publish a terminal event so the stream ends.
	go func() {
		time.Sleep(50 * time.Millisecond)
		b.Publish("task42", EventTaskDone, "s", map[string]string{"assetId": "a1"})
	}()

	reader := bufio.NewReader(resp.Body)
	var sawQueued, sawDone bool
	deadline := time.After(3 * time.Second)
	lines := make(chan string)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				close(lines)
				return
			}
			lines <- line
		}
	}()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				goto done
			}
			if strings.Contains(line, string(EventTaskQueued)) {
				sawQueued = true
			}
			if strings.Contains(line, string(EventTaskDone)) {
				sawDone = true
				goto done
			}
		case <-deadline:
			goto done
		}
	}
done:
	if !sawQueued || !sawDone {
		t.Fatalf("expected queued and done events; queued=%v done=%v", sawQueued, sawDone)
	}
}

func TestWebSocketRoundTrip(t *testing.T) {
	received := make(chan Inbound, 1)
	hub := NewHub(func(ctx context.Context, sessionID string, msg Inbound) {
		received <- msg
		// Echo a reply back to the session.
		hubReply(ctx, sessionID, msg)
	})
	// hubReply needs the hub; assign via closure variable.
	hubRef = hub

	mux := http.NewServeMux()
	RegisterRoutes(mux, hub, NewTaskBroker())
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws?session=s1"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.CloseNow()

	// Wait for the connection to register.
	for i := 0; i < 50 && hub.ConnCount("s1") == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ConnCount("s1") != 1 {
		t.Fatalf("expected 1 connection, got %d", hub.ConnCount("s1"))
	}

	// Send a user message.
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"type":"user_message","text":"hi"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-received:
		if msg.Text != "hi" {
			t.Fatalf("unexpected inbound: %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not receive message")
	}

	// Expect the echoed reply on the socket.
	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if !strings.Contains(string(data), "echo") {
		t.Fatalf("unexpected reply: %s", data)
	}
}

// hubRef and hubReply support the round-trip test's echo.
var hubRef *Hub

func hubReply(_ context.Context, sessionID string, msg Inbound) {
	hubRef.Send(sessionID, Event{Type: EventMessage, Data: map[string]string{"echo": msg.Text}})
}

func TestInboundCapsuleSelectParsing(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantType string
		wantText string
		wantSel  []string
	}{
		{
			name:     "edited free text",
			raw:      `{"type":"capsule_select","text":"把背景换成黄昏的海边"}`,
			wantType: "capsule_select",
			wantText: "把背景换成黄昏的海边",
		},
		{
			name:     "option value selection",
			raw:      `{"type":"capsule_select","selection":["change_background"]}`,
			wantType: "capsule_select",
			wantSel:  []string{"change_background"},
		},
		{
			name:     "selection with ref",
			raw:      `{"type":"capsule_select","selection":["a"],"ref":"asset_1"}`,
			wantType: "capsule_select",
			wantSel:  []string{"a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var msg Inbound
			if err := json.Unmarshal([]byte(tc.raw), &msg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if msg.Type != tc.wantType {
				t.Errorf("type = %q, want %q", msg.Type, tc.wantType)
			}
			if msg.Text != tc.wantText {
				t.Errorf("text = %q, want %q", msg.Text, tc.wantText)
			}
			if len(msg.Selection) != len(tc.wantSel) {
				t.Errorf("selection = %v, want %v", msg.Selection, tc.wantSel)
			}
		})
	}
}
