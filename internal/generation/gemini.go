package generation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gameasset/internal/config"
)

// GeminiProvider talks to Google's Gemini image models over the native
// generateContent API as exposed by the yunwu proxy. Both text-to-image and
// image+text (edit/reference) requests use the same endpoint; an inline image
// part is appended when a source/reference image is supplied.
//
// Request:  POST {base}/v1beta/models/{model}:generateContent
// Body:     {"contents":[{"parts":[{"text":...},{"inlineData":{mimeType,data}}]}]}
// Response: candidates[].content.parts[].inlineData{mimeType,data(base64)}
//
// Field casing is camelCase (verified against the yunwu proxy's live response).
// If the proxy instead exposes Gemini as an OpenAI-compatible /v1/images
// endpoint, configure Provider=openai to reuse HTTPProvider with zero code change.
type GeminiProvider struct {
	name    string
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewGeminiProvider builds a Gemini image provider from config. baseURL defaults
// to the Google Generative Language base when empty.
//
// The Gemini adapter appends "/v1beta/models/...", so the base must NOT already
// carry an API-version segment. Gemini commonly inherits the shared
// COMMON_BASE_URL (e.g. "https://yunwu.ai/v1") that is meant for the OpenAI-style
// backends; left as-is that would yield ".../v1/v1beta/..." → 404. Strip a
// trailing "/v1" or "/v1beta" so either form of base resolves correctly.
func NewGeminiProvider(cfg config.ImageProviderConfig) *GeminiProvider {
	base := strings.TrimRight(cfg.BaseURL, "/")
	base = strings.TrimSuffix(base, "/v1beta")
	base = strings.TrimSuffix(base, "/v1")
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	return &GeminiProvider{
		name:    cfg.Name,
		baseURL: base,
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// Name implements Provider.
func (p *GeminiProvider) Name() string { return p.name }

// geminiPart is one content part: either text or inline image data.
//
// Field tags use camelCase (inlineData/mimeType) — the canonical Gemini JSON the
// API actually returns. snake_case (inline_data) decodes to nothing on the
// response side, which surfaced as a misleading "empty image data" error. Google
// accepts camelCase on the request side too, so the same struct serves both.
type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inlineData,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64-encoded image bytes
}

type geminiRequest struct {
	Contents []struct {
		Parts []geminiPart `json:"parts"`
	} `json:"contents"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Generate implements Provider.
func (p *GeminiProvider) Generate(ctx context.Context, req Request) (Output, error) {
	if p.apiKey == "" {
		return Output{}, fmt.Errorf("provider %s: missing API key", p.name)
	}

	parts := []geminiPart{{Text: req.Prompt}}
	// Primary source image (image+text edit), then any extra references, each as
	// an inline_data part following the prompt.
	if len(req.SourceImage) > 0 {
		parts = append(parts, geminiPart{InlineData: &geminiInlineData{
			MimeType: mimeOrPNG(req.SourceMime),
			Data:     base64.StdEncoding.EncodeToString(req.SourceImage),
		}})
	}
	for _, ref := range req.ReferenceImages {
		if len(ref) == 0 {
			continue
		}
		parts = append(parts, geminiPart{InlineData: &geminiInlineData{
			MimeType: "image/png",
			Data:     base64.StdEncoding.EncodeToString(ref),
		}})
	}

	var body geminiRequest
	body.Contents = append(body.Contents, struct {
		Parts []geminiPart `json:"parts"`
	}{Parts: parts})
	buf, _ := json.Marshal(body)

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", p.baseURL, p.model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return Output{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// yunwu proxies typically accept a Bearer token; the native Google API uses
	// ?key= or x-goog-api-key. Send both so either proxy form authenticates.
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("x-goog-api-key", p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return Output{}, fmt.Errorf("provider %s: request: %w", p.name, err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return Output{}, fmt.Errorf("provider %s: status %d: %s", p.name, resp.StatusCode, truncate(string(raw), 300))
	}
	if readErr != nil {
		return Output{}, fmt.Errorf("provider %s: read response body: %w", p.name, readErr)
	}

	var parsed geminiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Output{}, fmt.Errorf("provider %s: decode response: %w", p.name, err)
	}
	if parsed.Error != nil {
		return Output{}, fmt.Errorf("provider %s: api error: %s", p.name, parsed.Error.Message)
	}
	img, mime := firstInlineImage(parsed)
	if img == "" {
		return Output{}, fmt.Errorf("provider %s: empty image data", p.name)
	}
	imgBytes, err := base64.StdEncoding.DecodeString(img)
	if err != nil {
		return Output{}, fmt.Errorf("provider %s: decode b64: %w", p.name, err)
	}
	return Output{Data: imgBytes, Mime: mimeOrPNG(mime)}, nil
}

// firstInlineImage returns the first inline image (base64) and its mime among
// the response candidates' parts.
func firstInlineImage(r geminiResponse) (data, mime string) {
	for _, c := range r.Candidates {
		for _, part := range c.Content.Parts {
			if part.InlineData != nil && part.InlineData.Data != "" {
				return part.InlineData.Data, part.InlineData.MimeType
			}
		}
	}
	return "", ""
}

// mimeOrPNG returns m when non-empty, else image/png.
func mimeOrPNG(m string) string {
	if m != "" {
		return m
	}
	return "image/png"
}
