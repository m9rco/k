package video

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gameasset/internal/config"
)

func veoCfg(url string) config.ImageProviderConfig {
	return config.ImageProviderConfig{
		Name: "veo", Provider: "veo", BaseURL: url,
		APIKey: "sk-x", Model: "veo_3_1_fast_components_vip",
	}
}

func TestVeoProviderFullFlow(t *testing.T) {
	var polls int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/video/generations", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"task-123"}`))
	})
	mux.HandleFunc("GET /v1/video/generations/task-123", func(w http.ResponseWriter, _ *http.Request) {
		// First poll: running. Second: succeeded with a url pointing back here.
		if atomic.AddInt32(&polls, 1) == 1 {
			_, _ = w.Write([]byte(`{"status":"RUNNING"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"SUCCEEDED","video_url":"` + veoFileURL + `"}`))
	})
	mux.HandleFunc("GET /file.mp4", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("MP4DATA"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	veoFileURL = srv.URL + "/file.mp4"

	p := NewVeoProvider(veoCfg(srv.URL))
	// Shorten poll cadence by using a context with the real 5s tick would be slow;
	// instead the second poll already succeeds, so one 5s wait occurs. Keep the
	// test fast by accepting that single tick.
	out, err := p.Generate(context.Background(), Request{Prompt: "walk", ImageURL: "https://img/x.png"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if string(out.Data) != "MP4DATA" {
		t.Errorf("video data = %q", out.Data)
	}
	if out.Mime != "video/mp4" {
		t.Errorf("mime = %q", out.Mime)
	}
}

var veoFileURL string

func TestVeoProviderNotConfigured(t *testing.T) {
	p := NewVeoProvider(config.ImageProviderConfig{Provider: "veo"}) // no key/model
	if p.Configured() {
		t.Fatal("expected not configured")
	}
	if _, err := p.Generate(context.Background(), Request{ImageURL: "x"}); err == nil {
		t.Fatal("expected not-configured error")
	}
}

func TestVeoProviderSubmitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad request"}`))
	}))
	defer srv.Close()
	p := NewVeoProvider(veoCfg(srv.URL))
	_, err := p.Generate(context.Background(), Request{Prompt: "x", ImageURL: "https://img/x.png"})
	if err == nil || !strings.Contains(err.Error(), "submit status") {
		t.Fatalf("expected submit status error, got %v", err)
	}
}

func TestNewVideoProviderSelectsAdapter(t *testing.T) {
	if _, ok := NewProvider(config.ImageProviderConfig{Provider: "veo"}).(*veoProvider); !ok {
		t.Error("Provider=veo should select veoProvider")
	}
	if _, ok := NewProvider(config.ImageProviderConfig{Provider: "happyhorse"}).(*httpProvider); !ok {
		t.Error("Provider=happyhorse should select httpProvider")
	}
	if _, ok := NewProvider(config.ImageProviderConfig{Provider: ""}).(*httpProvider); !ok {
		t.Error("empty Provider should default to httpProvider")
	}
}
