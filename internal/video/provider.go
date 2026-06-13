package video

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"gameasset/internal/config"
)

// httpProvider talks to the happyhorse image/reference-to-video API as exposed by
// the yunwu proxy (DashScope-compatible). The flow is asynchronous:
//
//  1. POST .../video-synthesis with a JSON body referencing the source image by
//     public URL and the motion prompt; the response carries an output.task_id.
//  2. Poll GET /v1/tasks/{task_id} until task_status is SUCCEEDED/FAILED.
//  3. Download the resulting mp4 from the result URL (no auth header).
//
// Gated by Configured() so an unset provider never half-runs.
type httpProvider struct {
	cfg    config.ImageProviderConfig
	client *http.Client
}

// NewHTTPProvider builds the happyhorse provider from config.
func NewHTTPProvider(cfg config.ImageProviderConfig) Provider {
	return &httpProvider{cfg: cfg, client: &http.Client{Timeout: 120 * time.Second}}
}

func (p *httpProvider) Name() string { return p.cfg.Name }

// Configured requires both an API key and a model id to be present.
func (p *httpProvider) Configured() bool {
	return p.cfg.APIKey != "" && p.cfg.Model != ""
}

// mediaType returns the per-image type tag the API expects for this model. The
// i2v model wants first_frame; the r2v (reference) model wants reference_image.
func (p *httpProvider) mediaType() string {
	if strings.Contains(p.cfg.Model, "r2v") {
		return "reference_image"
	}
	return "first_frame"
}

func (p *httpProvider) Generate(ctx context.Context, req Request) (Output, error) {
	if !p.Configured() {
		return Output{}, fmt.Errorf("video provider %s not configured", p.cfg.Name)
	}
	if req.ImageURL == "" {
		return Output{}, fmt.Errorf("video provider %s: missing source image url", p.cfg.Name)
	}
	base := strings.TrimRight(p.cfg.BaseURL, "/")
	if base == "" {
		return Output{}, fmt.Errorf("video provider %s missing base url", p.cfg.Name)
	}

	taskID, err := p.submit(ctx, base, req)
	if err != nil {
		return Output{}, err
	}
	log.Printf("video provider %s: submitted task=%s", p.cfg.Name, taskID)

	resultURL, err := p.poll(ctx, base, taskID)
	if err != nil {
		return Output{}, err
	}
	log.Printf("video provider %s: task=%s succeeded, fetching %s", p.cfg.Name, taskID, resultURL)

	vid, err := p.fetch(ctx, resultURL)
	if err != nil {
		return Output{}, err
	}
	return Output{Data: vid, Mime: "video/mp4", Provider: p.cfg.Name}, nil
}

// submit posts the synthesis request and returns the async task id.
func (p *httpProvider) submit(ctx context.Context, base string, req Request) (string, error) {
	body := map[string]any{
		"model": p.cfg.Model,
		"input": map[string]any{
			"prompt": req.Prompt,
			"media": []map[string]any{
				{"type": p.mediaType(), "url": req.ImageURL},
			},
		},
		"parameters": map[string]any{
			"resolution": "720P",
			"duration":   5,
		},
	}
	buf, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/api/v1/services/aigc/video-generation/video-synthesis", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("X-DashScope-Async", "enable")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("video provider %s: submit: %w", p.cfg.Name, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("video provider %s: submit status %d: %s", p.cfg.Name, resp.StatusCode, truncate(string(raw), 300))
	}
	var parsed struct {
		Output struct {
			TaskID     string `json:"task_id"`
			TaskStatus string `json:"task_status"`
		} `json:"output"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("video provider %s: decode submit response: %w (body: %s)", p.cfg.Name, err, truncate(string(raw), 200))
	}
	if parsed.Output.TaskID == "" {
		return "", fmt.Errorf("video provider %s: no task_id (code=%s msg=%s)", p.cfg.Name, parsed.Code, parsed.Message)
	}
	return parsed.Output.TaskID, nil
}

// poll queries the task until it reaches a terminal state, returning the result
// video URL on success. It tolerates several response shapes for the URL since
// the proxy's exact field placement is not fully documented.
func (p *httpProvider) poll(ctx context.Context, base, taskID string) (string, error) {
	deadline := time.Now().Add(5 * time.Minute)
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("video provider %s: task %s timed out", p.cfg.Name, taskID)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/tasks/"+taskID, nil)
		if err != nil {
			return "", err
		}
		httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
		resp, err := p.client.Do(httpReq)
		if err != nil {
			return "", fmt.Errorf("video provider %s: poll: %w", p.cfg.Name, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return "", fmt.Errorf("video provider %s: poll status %d: %s", p.cfg.Name, resp.StatusCode, truncate(string(raw), 300))
		}

		var parsed struct {
			Output struct {
				TaskStatus string `json:"task_status"`
				// Result URL appears under different keys across DashScope-style
				// responses; accept the common ones.
				VideoURL  string `json:"video_url"`
				ResultURL string `json:"result_url"`
				Results   []struct {
					URL      string `json:"url"`
					VideoURL string `json:"video_url"`
				} `json:"results"`
				Message string `json:"message"`
			} `json:"output"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return "", fmt.Errorf("video provider %s: decode poll response: %w (body: %s)", p.cfg.Name, err, truncate(string(raw), 200))
		}
		switch strings.ToUpper(parsed.Output.TaskStatus) {
		case "SUCCEEDED":
			if u := firstVideoURL(parsed.Output.VideoURL, parsed.Output.ResultURL, parsed.Output.Results); u != "" {
				return u, nil
			}
			return "", fmt.Errorf("video provider %s: succeeded but no result url (body: %s)", p.cfg.Name, truncate(string(raw), 300))
		case "FAILED", "CANCELED", "UNKNOWN":
			return "", fmt.Errorf("video provider %s: task %s %s: %s", p.cfg.Name, taskID, parsed.Output.TaskStatus, parsed.Output.Message)
		default:
			// PENDING / RUNNING: keep polling.
		}
	}
}

// firstVideoURL picks the first non-empty URL among the accepted shapes.
func firstVideoURL(videoURL, resultURL string, results []struct {
	URL      string `json:"url"`
	VideoURL string `json:"video_url"`
}) string {
	if videoURL != "" {
		return videoURL
	}
	if resultURL != "" {
		return resultURL
	}
	for _, r := range results {
		if r.URL != "" {
			return r.URL
		}
		if r.VideoURL != "" {
			return r.VideoURL
		}
	}
	return ""
}

// fetch downloads a hosted video URL. No Authorization header per the provider's
// contract (result links are pre-signed and expire in 24h).
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
