package generation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gameasset/internal/config"
)

func TestDashScopeProviderFullFlow(t *testing.T) {
	var polls int32
	var resultURL string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/services/aigc/text2image/image-synthesis", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-DashScope-Async") != "enable" {
			t.Errorf("missing async header")
		}
		_, _ = w.Write([]byte(`{"output":{"task_id":"t-1"}}`))
	})
	mux.HandleFunc("GET /api/v1/tasks/t-1", func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&polls, 1) == 1 {
			_, _ = w.Write([]byte(`{"output":{"task_status":"RUNNING"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"output":{"task_status":"SUCCEEDED","results":[{"url":"` + resultURL + `"}]}}`))
	})
	mux.HandleFunc("GET /img.png", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("PNGBYTES"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resultURL = srv.URL + "/img.png"

	p := NewDashScopeProvider(config.ImageProviderConfig{
		Name: "t2i", Provider: "dashscope", BaseURL: srv.URL,
		APIKey: "sk-x", Model: "wan2.7-image-pro",
	})
	p.pollInterval = 10 * time.Millisecond // keep the test fast

	out, err := p.Generate(context.Background(), Request{Prompt: "a dragon", Width: 1024, Height: 1024})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if string(out.Data) != "PNGBYTES" {
		t.Errorf("image data = %q", out.Data)
	}
}

func TestDashScopeProviderErrors(t *testing.T) {
	t.Run("missing key", func(t *testing.T) {
		p := NewDashScopeProvider(config.ImageProviderConfig{Model: "m"})
		if _, err := p.Generate(context.Background(), Request{Prompt: "x"}); err == nil {
			t.Fatal("expected missing-key error")
		}
	})
	t.Run("task failed", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /api/v1/services/aigc/text2image/image-synthesis", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"output":{"task_id":"t-2"}}`))
		})
		mux.HandleFunc("GET /api/v1/tasks/t-2", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"output":{"task_status":"FAILED","message":"nsfw"}}`))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		p := NewDashScopeProvider(config.ImageProviderConfig{BaseURL: srv.URL, APIKey: "k", Model: "m"})
		p.pollInterval = 10 * time.Millisecond
		_, err := p.Generate(context.Background(), Request{Prompt: "x"})
		if err == nil || !strings.Contains(err.Error(), "FAILED") {
			t.Fatalf("expected FAILED error, got %v", err)
		}
	})
}

func TestNewProviderSelectsDashScope(t *testing.T) {
	if _, ok := NewProvider(config.ImageProviderConfig{Provider: "dashscope"}).(*DashScopeProvider); !ok {
		t.Error("Provider=dashscope should select DashScopeProvider")
	}
}

func TestBuildPromptTextToImage(t *testing.T) {
	p, err := BuildPrompt(Slots{Kind: EditTextToImage, TextToImageDesc: "a neon city"}, nil)
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	if !strings.Contains(p, "a neon city") {
		t.Errorf("prompt missing desc: %q", p)
	}
	// No source => no harmony/palette clause.
	if strings.Contains(p, "source image") {
		t.Errorf("text-to-image prompt should not reference a source image: %q", p)
	}
	if _, err := BuildPrompt(Slots{Kind: EditTextToImage}, nil); err == nil {
		t.Error("expected error for empty text-to-image desc")
	}
}
