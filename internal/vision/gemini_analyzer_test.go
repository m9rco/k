package vision

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var onePNG = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// TestGeminiAnalyzerInlineNoURL verifies the analyzer sends inline image data
// (no public URL) to :generateContent and parses the text parts as the report.
func TestGeminiAnalyzerInlineNoURL(t *testing.T) {
	var gotInline bool
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Contents []struct {
				Parts []struct {
					Text       string `json:"text"`
					InlineData *struct {
						MimeType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"contents"`
		}
		_ = json.Unmarshal(body, &req)
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				if p.InlineData != nil && p.InlineData.Data != "" {
					gotInline = true
				}
			}
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"核心主题：测试游戏"}]}}]}`))
	}))
	defer srv.Close()

	a := NewGemini(srv.URL, "sk-x", "gemini-2.5-flash-all")
	if a == nil || !a.Configured() {
		t.Fatal("expected configured analyzer")
	}
	if a.NeedsPublicURL() {
		t.Error("gemini analyzer must NOT need a public URL")
	}
	report, err := a.Analyze(context.Background(), []Image{{Data: onePNG, Mime: "image/png"}}, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !gotInline {
		t.Error("expected inline image data in request")
	}
	if !strings.HasSuffix(gotPath, ":generateContent") {
		t.Errorf("expected :generateContent path, got %q", gotPath)
	}
	if !strings.Contains(report, "核心主题") {
		t.Errorf("unexpected report: %q", report)
	}
}

func TestGeminiAnalyzerDisabledWithoutKey(t *testing.T) {
	if NewGemini("https://x", "", "") != nil {
		t.Error("expected nil analyzer without apiKey")
	}
}

func TestOpenAIAnalyzerNeedsURL(t *testing.T) {
	a := NewOpenAI("https://x", "k", "grok-4-fast")
	if a == nil || !a.NeedsPublicURL() {
		t.Fatal("openai analyzer must need a public URL")
	}
	// No URL on the image → error (no inline fallback on this path).
	if _, err := a.Analyze(context.Background(), []Image{{Data: onePNG}}, nil); err == nil {
		t.Error("expected error when no URL provided to openai analyzer")
	}
}
