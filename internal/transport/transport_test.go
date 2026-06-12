package transport

import (
	"bufio"
	"context"
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
