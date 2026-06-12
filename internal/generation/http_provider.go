package generation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"gameasset/internal/config"
)

// HTTPProvider talks to an OpenAI-compatible image API (gpt-image-1). It uses
// the images/edits endpoint when a source image is supplied, otherwise
// images/generations. Responses are expected in b64_json form.
type HTTPProvider struct {
	name    string
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewHTTPProvider builds a provider from config. baseURL defaults to the OpenAI
// public endpoint when empty.
func NewHTTPProvider(cfg config.ImageProviderConfig) *HTTPProvider {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return &HTTPProvider{
		name:    cfg.Name,
		baseURL: base,
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// Name implements Provider.
func (p *HTTPProvider) Name() string { return p.name }

type imageAPIResponse struct {
	Data []struct {
		B64JSON string `json:"b64_json"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Generate implements Provider.
func (p *HTTPProvider) Generate(ctx context.Context, req Request) (Output, error) {
	if p.apiKey == "" {
		return Output{}, fmt.Errorf("provider %s: missing API key", p.name)
	}
	var (
		httpReq *http.Request
		err     error
	)
	if len(req.SourceImage) > 0 {
		httpReq, err = p.buildEditRequest(ctx, req)
	} else {
		httpReq, err = p.buildGenerateRequest(ctx, req)
	}
	if err != nil {
		return Output{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return Output{}, fmt.Errorf("provider %s: request: %w", p.name, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return Output{}, fmt.Errorf("provider %s: status %d: %s", p.name, resp.StatusCode, truncate(string(body), 300))
	}
	var parsed imageAPIResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Output{}, fmt.Errorf("provider %s: decode response: %w", p.name, err)
	}
	if parsed.Error != nil {
		return Output{}, fmt.Errorf("provider %s: api error: %s", p.name, parsed.Error.Message)
	}
	if len(parsed.Data) == 0 || parsed.Data[0].B64JSON == "" {
		return Output{}, fmt.Errorf("provider %s: empty image data", p.name)
	}
	imgBytes, err := base64.StdEncoding.DecodeString(parsed.Data[0].B64JSON)
	if err != nil {
		return Output{}, fmt.Errorf("provider %s: decode b64: %w", p.name, err)
	}
	return Output{Data: imgBytes, Mime: "image/png"}, nil
}

func (p *HTTPProvider) buildGenerateRequest(ctx context.Context, req Request) (*http.Request, error) {
	payload := map[string]any{
		"model":  p.model,
		"prompt": req.Prompt,
		"n":      1,
	}
	if sz := sizeParam(req.Width, req.Height); sz != "" {
		payload["size"] = sz
	}
	buf, _ := json.Marshal(payload)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/images/generations", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	return r, nil
}

func (p *HTTPProvider) buildEditRequest(ctx context.Context, req Request) (*http.Request, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("model", p.model)
	_ = mw.WriteField("prompt", req.Prompt)
	_ = mw.WriteField("n", "1")
	if sz := sizeParam(req.Width, req.Height); sz != "" {
		_ = mw.WriteField("size", sz)
	}
	fw, err := mw.CreateFormFile("image", "source.png")
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(req.SourceImage); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/images/edits", &body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r, nil
}

// sizeParam maps dimensions to the gpt-image-1 size enum, snapping to the
// nearest supported value. Empty means "let the provider decide".
func sizeParam(w, h int) string {
	if w == 0 || h == 0 {
		return ""
	}
	switch {
	case w == h:
		return "1024x1024"
	case w > h:
		return "1536x1024"
	default:
		return "1024x1536"
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
