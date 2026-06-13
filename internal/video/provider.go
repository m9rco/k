package video

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

// httpProvider talks to the happyhorse R2V (reference-to-video) endpoint over an
// OpenAI-compatible-ish multipart API. The exact provider contract is pending
// confirmation (see change design Open Questions); this implementation submits
// the source image + prompt and decodes a base64 / URL video from the response,
// and is gated by Configured() so an unset provider never half-runs.
type httpProvider struct {
	cfg    config.ImageProviderConfig
	client *http.Client
}

// NewHTTPProvider builds the happyhorse R2V provider from config.
func NewHTTPProvider(cfg config.ImageProviderConfig) Provider {
	return &httpProvider{cfg: cfg, client: &http.Client{Timeout: 300 * time.Second}}
}

func (p *httpProvider) Name() string { return p.cfg.Name }

// Configured requires both an API key and a model id to be present.
func (p *httpProvider) Configured() bool {
	return p.cfg.APIKey != "" && p.cfg.Model != ""
}

func (p *httpProvider) Generate(ctx context.Context, req Request) (Output, error) {
	if !p.Configured() {
		return Output{}, fmt.Errorf("video provider %s not configured", p.cfg.Name)
	}
	base := strings.TrimRight(p.cfg.BaseURL, "/")
	if base == "" {
		return Output{}, fmt.Errorf("video provider %s missing base url", p.cfg.Name)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("model", p.cfg.Model)
	_ = mw.WriteField("prompt", req.Prompt)
	fw, err := mw.CreateFormFile("image", "source.png")
	if err != nil {
		return Output{}, err
	}
	if _, err := fw.Write(req.SourceImage); err != nil {
		return Output{}, err
	}
	if err := mw.Close(); err != nil {
		return Output{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/videos/generations", &body)
	if err != nil {
		return Output{}, err
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return Output{}, fmt.Errorf("video provider %s: request: %w", p.cfg.Name, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return Output{}, fmt.Errorf("video provider %s: status %d: %s", p.cfg.Name, resp.StatusCode, truncate(string(raw), 300))
	}

	// Accept either b64_json video data or a hosted url in the response.
	var parsed struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Output{}, fmt.Errorf("video provider %s: decode response: %w", p.cfg.Name, err)
	}
	if parsed.Error != nil {
		return Output{}, fmt.Errorf("video provider %s: api error: %s", p.cfg.Name, parsed.Error.Message)
	}
	if len(parsed.Data) == 0 {
		return Output{}, fmt.Errorf("video provider %s: empty response", p.cfg.Name)
	}
	d := parsed.Data[0]
	if d.B64JSON != "" {
		vid, err := base64.StdEncoding.DecodeString(d.B64JSON)
		if err != nil {
			return Output{}, fmt.Errorf("video provider %s: decode b64: %w", p.cfg.Name, err)
		}
		return Output{Data: vid, Mime: "video/mp4", Provider: p.cfg.Name}, nil
	}
	if d.URL != "" {
		vid, err := p.fetch(ctx, d.URL)
		if err != nil {
			return Output{}, err
		}
		return Output{Data: vid, Mime: "video/mp4", Provider: p.cfg.Name}, nil
	}
	return Output{}, fmt.Errorf("video provider %s: response had no video data", p.cfg.Name)
}

// fetch downloads a hosted video URL returned by the provider.
func (p *httpProvider) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch video url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("fetch video url: status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
