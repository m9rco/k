package generation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gameasset/internal/config"
)

// pngPixel is a 1x1 PNG used as a stand-in image payload.
var geminiPNG = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

func TestGeminiProviderGenerateSuccess(t *testing.T) {
	want := base64.StdEncoding.EncodeToString(geminiPNG)
	var gotPath string
	var gotBody geminiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		resp := geminiResponse{}
		resp.Candidates = append(resp.Candidates, struct {
			Content struct {
				Parts []geminiPart `json:"parts"`
			} `json:"content"`
		}{})
		resp.Candidates[0].Content.Parts = []geminiPart{
			{InlineData: &geminiInlineData{MimeType: "image/png", Data: want}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewGeminiProvider(config.ImageProviderConfig{
		Name: "gemini", Provider: "gemini", BaseURL: srv.URL,
		APIKey: "sk-x", Model: "gemini-3-pro-image",
	})
	out, err := p.Generate(context.Background(), Request{
		Prompt:      "a cat",
		SourceImage: geminiPNG,
		SourceMime:  "image/png",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if string(out.Data) != string(geminiPNG) {
		t.Errorf("decoded image mismatch")
	}
	if out.Mime != "image/png" {
		t.Errorf("Mime = %q, want image/png", out.Mime)
	}
	if !strings.Contains(gotPath, "gemini-3-pro-image:generateContent") {
		t.Errorf("path = %q, want model:generateContent", gotPath)
	}
	// Prompt + 1 inline source image part.
	if len(gotBody.Contents) != 1 || len(gotBody.Contents[0].Parts) != 2 {
		t.Fatalf("expected 1 content with 2 parts, got %+v", gotBody.Contents)
	}
	if gotBody.Contents[0].Parts[0].Text != "a cat" {
		t.Errorf("first part text = %q", gotBody.Contents[0].Parts[0].Text)
	}
	if gotBody.Contents[0].Parts[1].InlineData == nil {
		t.Error("second part should carry inline source image")
	}
}

func TestGeminiProviderTextOnlyAndRefs(t *testing.T) {
	want := base64.StdEncoding.EncodeToString(geminiPNG)
	var gotBody geminiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		resp := geminiResponse{}
		resp.Candidates = append(resp.Candidates, struct {
			Content struct {
				Parts []geminiPart `json:"parts"`
			} `json:"content"`
		}{})
		resp.Candidates[0].Content.Parts = []geminiPart{
			{InlineData: &geminiInlineData{MimeType: "image/png", Data: want}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewGeminiProvider(config.ImageProviderConfig{
		Name: "gemini", BaseURL: srv.URL, APIKey: "sk-x", Model: "gemini-2.5-flash-image",
	})
	// No source, two reference images => prompt + 2 inline parts.
	_, err := p.Generate(context.Background(), Request{
		Prompt:          "scene",
		ReferenceImages: [][]byte{geminiPNG, geminiPNG},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(gotBody.Contents[0].Parts) != 3 {
		t.Fatalf("expected prompt + 2 refs = 3 parts, got %d", len(gotBody.Contents[0].Parts))
	}
}

func TestGeminiProviderErrors(t *testing.T) {
	t.Run("missing key", func(t *testing.T) {
		p := NewGeminiProvider(config.ImageProviderConfig{Model: "m"})
		if _, err := p.Generate(context.Background(), Request{Prompt: "x"}); err == nil {
			t.Fatal("expected error for missing key")
		}
	})
	t.Run("api error body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"error":{"message":"bad model"}}`))
		}))
		defer srv.Close()
		p := NewGeminiProvider(config.ImageProviderConfig{BaseURL: srv.URL, APIKey: "k", Model: "m"})
		if _, err := p.Generate(context.Background(), Request{Prompt: "x"}); err == nil {
			t.Fatal("expected api error")
		}
	})
	t.Run("empty data", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"candidates":[]}`))
		}))
		defer srv.Close()
		p := NewGeminiProvider(config.ImageProviderConfig{BaseURL: srv.URL, APIKey: "k", Model: "m"})
		if _, err := p.Generate(context.Background(), Request{Prompt: "x"}); err == nil {
			t.Fatal("expected empty-data error")
		}
	})
}

func TestNewProviderSelectsAdapter(t *testing.T) {
	if _, ok := NewProvider(config.ImageProviderConfig{Provider: "gemini"}).(*GeminiProvider); !ok {
		t.Error("Provider=gemini should select GeminiProvider")
	}
	if _, ok := NewProvider(config.ImageProviderConfig{Provider: "openai"}).(*HTTPProvider); !ok {
		t.Error("Provider=openai should select HTTPProvider")
	}
	if _, ok := NewProvider(config.ImageProviderConfig{Provider: ""}).(*HTTPProvider); !ok {
		t.Error("empty Provider should default to HTTPProvider")
	}
	if _, ok := NewProvider(config.ImageProviderConfig{Provider: "unknown-xyz"}).(*HTTPProvider); !ok {
		t.Error("unknown Provider should fall back to HTTPProvider")
	}
}
