package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// geminiAnalyzer produces the marketing-analysis report via Google's Gemini
// native generateContent API with INLINE image data (inlineData base64). Unlike
// the OpenAI-compatible image_url path, it does not require the image to be
// published to a public URL first — the bytes are sent inline — so the vision
// pre-stage no longer hard-depends on COS.
//
// Request:  POST {base}/v1beta/models/{model}:generateContent
// Body:     {"contents":[{"parts":[{"text":...},{"inlineData":{mimeType,data}}...]}]}
// Response: candidates[].content.parts[].text
//
// The request mirrors generation.GeminiProvider's verified camelCase shape; the
// only difference is the response side reads text parts (analysis) instead of
// inline image parts (generation).
type geminiAnalyzer struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewGemini returns a Gemini-native inline analyzer. Returns nil when apiKey is
// empty (caller treats nil as "not configured"). baseURL defaults to the Google
// Generative Language endpoint; a trailing /v1 or /v1beta is stripped so a base
// shared with the OpenAI-style gateways still resolves correctly.
func NewGemini(baseURL, apiKey, model string) Analyzer {
	if strings.TrimSpace(apiKey) == "" {
		return nil
	}
	base := strings.TrimRight(baseURL, "/")
	base = strings.TrimSuffix(base, "/v1beta")
	base = strings.TrimSuffix(base, "/v1")
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	if model == "" {
		model = "gemini-2.5-flash-all"
	}
	return &geminiAnalyzer{
		baseURL: base,
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// Configured reports whether the analyzer is ready to use.
func (a *geminiAnalyzer) Configured() bool { return a != nil }

// NeedsPublicURL is false: the inline path reads Image.Data directly.
func (a *geminiAnalyzer) NeedsPublicURL() bool { return false }

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64-encoded image bytes
}

type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inlineData,omitempty"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}

// Analyze sends the fixed analysis prompt plus each image's inline bytes to
// Gemini and returns the report text. The Gemini generateContent endpoint is not
// streamed here (the report is short and the caller's onChunk only drives a
// progress panel), so onChunk — when non-nil — is invoked once with the full
// report so the existing streaming-panel UX still receives the text. Graceful on
// errors: returns ("", err) so callers degrade cleanly.
func (a *geminiAnalyzer) Analyze(ctx context.Context, images []Image, onChunk func(string)) (string, error) {
	if a == nil {
		return "", fmt.Errorf("vision: analyzer not configured")
	}
	parts := make([]geminiPart, 0, len(images)+1)
	parts = append(parts, geminiPart{Text: analysisPrompt})
	for _, im := range images {
		if len(im.Data) == 0 {
			continue
		}
		parts = append(parts, geminiPart{InlineData: &geminiInlineData{
			MimeType: mimeOrPNG(im.Mime),
			Data:     base64.StdEncoding.EncodeToString(im.Data),
		}})
	}
	if len(parts) == 1 {
		return "", fmt.Errorf("vision: no inline images provided")
	}

	body := geminiRequest{Contents: []geminiContent{{Parts: parts}}}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("vision: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", a.baseURL, a.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("vision: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// yunwu proxies typically accept a Bearer token; the native Google API uses
	// x-goog-api-key. Send both so either proxy form authenticates.
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("x-goog-api-key", a.apiKey)

	log.Printf("vision.analyze(gemini): model=%s images=%d endpoint=%s/v1beta/models/%s:generateContent",
		a.model, len(images), a.baseURL, a.model)

	start := time.Now()
	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vision: request: %w", err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vision: status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	if readErr != nil {
		return "", fmt.Errorf("vision: read response body: %w", readErr)
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []geminiPart `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("vision: decode response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("vision: api error: %s", parsed.Error.Message)
	}
	var full strings.Builder
	for _, c := range parsed.Candidates {
		for _, p := range c.Content.Parts {
			if p.Text != "" {
				full.WriteString(p.Text)
			}
		}
	}
	report := full.String()
	// Truncate on a rune boundary so a multi-byte glyph is never cut into an
	// invalid replacement char carried into the generation prompt's THEME.
	if r := []rune(report); len(r) > maxReportLen {
		report = string(r[:maxReportLen])
	}
	if report == "" {
		return "", fmt.Errorf("vision: empty analysis text")
	}
	if onChunk != nil {
		onChunk(report)
	}
	log.Printf("vision.analyze(gemini): done in %s report_len=%d runes=%d",
		time.Since(start), len(report), utf8.RuneCountInString(report))
	return report, nil
}

// mimeOrPNG returns m when non-empty, else image/png.
func mimeOrPNG(m string) string {
	if m != "" {
		return m
	}
	return "image/png"
}

// truncate caps s at n bytes for error messages (best-effort, not rune-aware).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
