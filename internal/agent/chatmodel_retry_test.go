package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"gameasset/internal/config"
)

// fastRetryModel builds a chatModel pointed at a test server, with a tiny retry
// backoff so the suite stays quick.
func fastRetryModel(baseURL string) *chatModel {
	return &chatModel{
		cfg:       config.ModelConfig{Provider: "openai", Model: "test-model", APIKey: "k", BaseURL: baseURL},
		client:    &http.Client{Timeout: 5 * time.Second},
		retryBase: 5 * time.Millisecond,
	}
}

func TestGenerateRetriesOn429ThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"RPM limit reached"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
	}))
	defer srv.Close()

	m := fastRetryModel(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := m.Generate(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("Generate after retries: %v", err)
	}
	if out.Content != "hello" {
		t.Errorf("content = %q, want hello", out.Content)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("expected 3 attempts (2x429 then ok), got %d", got)
	}
}

func TestGenerateGivesUpAfterMaxRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"RPM limit reached"}}`))
	}))
	defer srv.Close()

	m := fastRetryModel(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	_, err := m.Generate(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if got := atomic.LoadInt32(&calls); got != maxRetries+1 {
		t.Errorf("expected %d attempts, got %d", maxRetries+1, got)
	}
}

func TestStreamDegradesWhenNotSSE(t *testing.T) {
	// Server returns 200 to the stream request but a buffered JSON body
	// (not text/event-stream); Stream must degrade to one-shot Generate.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"degraded ok"}}]}`))
	}))
	defer srv.Close()

	m := fastRetryModel(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sr, err := m.Stream(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()
	var got strings.Builder
	for {
		msg, err := sr.Recv()
		if err != nil {
			break
		}
		if msg != nil {
			got.WriteString(msg.Content)
		}
	}
	if got.String() != "degraded ok" {
		t.Errorf("degraded stream content = %q, want %q", got.String(), "degraded ok")
	}
}

func TestChunkRunes(t *testing.T) {
	cases := []struct {
		in   string
		size int
		want int // expected number of fragments
	}{
		{"", 24, 0},
		{"短", 24, 1},
		{strings.Repeat("字", 24), 24, 1},
		{strings.Repeat("字", 25), 24, 2},
		{strings.Repeat("字", 50), 24, 3},
	}
	for _, c := range cases {
		got := chunkRunes(c.in, c.size)
		if len(got) != c.want {
			t.Errorf("chunkRunes(%d runes, %d) = %d frags, want %d", len([]rune(c.in)), c.size, len(got), c.want)
		}
		// Rejoining must reproduce the input exactly (rune-safe, no loss).
		if strings.Join(got, "") != c.in {
			t.Errorf("chunkRunes rejoin mismatch for %q", c.in)
		}
	}
}

func TestDegradedStreamChunksReasoning(t *testing.T) {
	// A non-SSE 200 with a long reasoning_content must degrade and re-emit the
	// reasoning as MULTIPLE frames (not one block), preserving the typewriter.
	longReason := strings.Repeat("思考", 40) // 80 runes -> >1 frame at size 24
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"答复","reasoning_content":"` + longReason + `"}}]}`))
	}))
	defer srv.Close()

	m := fastRetryModel(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sr, err := m.Stream(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()
	reasonFrames := 0
	var reason, content strings.Builder
	for {
		msg, err := sr.Recv()
		if err != nil {
			break
		}
		if msg == nil {
			continue
		}
		if msg.ReasoningContent != "" {
			reasonFrames++
			reason.WriteString(msg.ReasoningContent)
		}
		content.WriteString(msg.Content)
	}
	if reasonFrames < 2 {
		t.Errorf("expected reasoning re-chunked into >=2 frames, got %d", reasonFrames)
	}
	if reason.String() != longReason {
		t.Errorf("reasoning content lost in re-chunk: got %d runes, want %d", len([]rune(reason.String())), len([]rune(longReason)))
	}
	if content.String() != "答复" {
		t.Errorf("content = %q, want 答复", content.String())
	}
}

func TestStreamFiresDegradeNotifierOnFallback(t *testing.T) {
	// Non-SSE 200 forces a degrade; the context's degrade notifier must fire.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"x"}}]}`))
	}))
	defer srv.Close()

	var fired int32
	ctx := withDegradeNotifier(context.Background(), func() { atomic.AddInt32(&fired, 1) })
	m := fastRetryModel(srv.URL)
	sr, err := m.Stream(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for {
		if _, err := sr.Recv(); err != nil {
			break
		}
	}
	sr.Close()
	if got := atomic.LoadInt32(&fired); got != 1 {
		t.Errorf("degrade notifier fired %d times, want 1", got)
	}
}

func TestStreamDoesNotFireDegradeNotifierOnHealthyStream(t *testing.T) {
	// A proper SSE stream must NOT trigger the degrade notifier.
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hi"}}]}`,
		`data: [DONE]`,
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	defer srv.Close()

	var fired int32
	ctx := withDegradeNotifier(context.Background(), func() { atomic.AddInt32(&fired, 1) })
	m := fastRetryModel(srv.URL)
	sr, err := m.Stream(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for {
		if _, err := sr.Recv(); err != nil {
			break
		}
	}
	sr.Close()
	if got := atomic.LoadInt32(&fired); got != 0 {
		t.Errorf("degrade notifier fired %d times on healthy stream, want 0", got)
	}
}

func TestDegradeNotifierSafeToCallRepeatedly(t *testing.T) {
	// The orchestrator's notifier is once-guarded so repeated degrades within a
	// turn (e.g. fake-ack retry) emit at most one signal. With no hub installed
	// emit is a no-op; this asserts the returned closure is safe to invoke many
	// times (sync.Once guard, no panic / re-entry).
	o := &Orchestrator{}
	fn := o.degradeNotifier("s1")
	fn()
	fn()
	fn()
}

// TestStreamOpenFailsFastTo OneShotOn503 asserts the design D1 fast-failover:
// when the streaming endpoint returns 503, Stream must NOT burn the full retry
// budget on it. It retries the stream open only streamOpenRetries times, then
// degrades to a one-shot Generate (which here succeeds), keeping the user's
// wait short on a proxy whose streaming endpoint is flaky.
func TestStreamOpenFailsFastToOneShotOn503(t *testing.T) {
	var streamOpens, oneShots int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The streaming request carries Accept: text/event-stream; the one-shot
		// fallback (Generate) does not — use that to tell the two paths apart.
		if strings.Contains(r.Header.Get("Accept"), "event-stream") {
			atomic.AddInt32(&streamOpens, 1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"upstream busy"}}`))
			return
		}
		atomic.AddInt32(&oneShots, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"fell back ok"}}]}`))
	}))
	defer srv.Close()

	var fired int32
	ctx := withDegradeNotifier(context.Background(), func() { atomic.AddInt32(&fired, 1) })
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	m := fastRetryModel(srv.URL)
	sr, err := m.Stream(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()
	var got strings.Builder
	for {
		msg, err := sr.Recv()
		if err != nil {
			break
		}
		if msg != nil {
			got.WriteString(msg.Content)
		}
	}
	if got.String() != "fell back ok" {
		t.Errorf("degraded content = %q, want %q", got.String(), "fell back ok")
	}
	// Stream open is tried at most streamOpenRetries+1 times — NOT maxRetries+1.
	if so := atomic.LoadInt32(&streamOpens); so != int32(streamOpenRetries+1) {
		t.Errorf("stream opens = %d, want %d (fast failover, not full %d-retry budget)", so, streamOpenRetries+1, maxRetries)
	}
	// The fallback one-shot must have been used exactly once.
	if os := atomic.LoadInt32(&oneShots); os != 1 {
		t.Errorf("one-shot fallback calls = %d, want 1", os)
	}
	// And the degrade notifier must fire so the frontend switches to static wait.
	if atomic.LoadInt32(&fired) != 1 {
		t.Errorf("degrade notifier fired %d times, want 1", atomic.LoadInt32(&fired))
	}
}
