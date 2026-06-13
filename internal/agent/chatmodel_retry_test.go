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
