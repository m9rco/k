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

// veoProvider talks to Google Veo image-to-video as exposed by the yunwu proxy.
// Like happyhorse it is asynchronous: submit a task referencing the source image
// by public URL + a motion prompt, poll until the task reaches a terminal state,
// then download the resulting mp4.
//
// Submit:  POST {base}/v1/video/generations   {model, prompt, image}
// Poll:    GET  {base}/v1/video/generations/{id}  -> {status, video_url}
//
// NOTE: the exact paths and field names the yunwu proxy uses for Veo could not be
// verified online at authoring time. The shapes below are deliberately tolerant
// (multiple status spellings and result-URL keys are accepted, mirroring the
// happyhorse adapter) and isolated here so they can be corrected against the live
// proxy without touching the service/factory layers.
type veoProvider struct {
	cfg    config.ImageProviderConfig
	client *http.Client
}

// NewVeoProvider builds the Veo provider from config.
func NewVeoProvider(cfg config.ImageProviderConfig) Provider {
	return &veoProvider{cfg: cfg, client: &http.Client{Timeout: 120 * time.Second}}
}

func (p *veoProvider) Name() string { return p.cfg.Name }

// Configured requires both an API key and a model id to be present.
func (p *veoProvider) Configured() bool {
	return p.cfg.APIKey != "" && p.cfg.Model != ""
}

func (p *veoProvider) Generate(ctx context.Context, req Request) (Output, error) {
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

// submit posts the generation request and returns the async task id.
func (p *veoProvider) submit(ctx context.Context, base string, req Request) (string, error) {
	body := map[string]any{
		"model":  p.cfg.Model,
		"prompt": req.Prompt,
		"image":  req.ImageURL,
	}
	buf, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/video/generations", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("video provider %s: submit: %w", p.cfg.Name, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("video provider %s: submit status %d: %s", p.cfg.Name, resp.StatusCode, truncate(string(raw), 300))
	}
	// Accept both a flat {id} and a nested {data:{id}} / {task_id} shape.
	var parsed struct {
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
		Data   struct {
			ID     string `json:"id"`
			TaskID string `json:"task_id"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("video provider %s: decode submit response: %w (body: %s)", p.cfg.Name, err, truncate(string(raw), 200))
	}
	id := firstNonEmpty(parsed.ID, parsed.TaskID, parsed.Data.ID, parsed.Data.TaskID)
	if id == "" {
		return "", fmt.Errorf("video provider %s: no task id (msg=%s)", p.cfg.Name, parsed.Message)
	}
	return id, nil
}

// poll queries the task until it reaches a terminal state, returning the result
// video URL on success.
func (p *veoProvider) poll(ctx context.Context, base, taskID string) (string, error) {
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

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/video/generations/"+taskID, nil)
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
			Status   string `json:"status"`
			VideoURL string `json:"video_url"`
			URL      string `json:"url"`
			Data     struct {
				Status   string `json:"status"`
				VideoURL string `json:"video_url"`
				URL      string `json:"url"`
			} `json:"data"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return "", fmt.Errorf("video provider %s: decode poll response: %w (body: %s)", p.cfg.Name, err, truncate(string(raw), 200))
		}
		status := strings.ToUpper(firstNonEmpty(parsed.Status, parsed.Data.Status))
		switch status {
		case "SUCCEEDED", "SUCCESS", "COMPLETED":
			if u := firstNonEmpty(parsed.VideoURL, parsed.URL, parsed.Data.VideoURL, parsed.Data.URL); u != "" {
				return u, nil
			}
			return "", fmt.Errorf("video provider %s: succeeded but no result url (body: %s)", p.cfg.Name, truncate(string(raw), 300))
		case "FAILED", "ERROR", "CANCELED", "CANCELLED":
			return "", fmt.Errorf("video provider %s: task %s %s: %s", p.cfg.Name, taskID, status, parsed.Message)
		default:
			// PENDING / RUNNING / QUEUED / empty: keep polling.
		}
	}
}

// fetch downloads a hosted video URL (pre-signed; no auth header).
func (p *veoProvider) fetch(ctx context.Context, url string) ([]byte, error) {
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

// firstNonEmpty returns the first non-empty string among the args.
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
