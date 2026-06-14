package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gameasset/internal/config"
)

// DashScopeProvider talks to Alibaba's wan/qwen text-to-image models as exposed
// by the yunwu proxy. These use DashScope's asynchronous task flow:
//
//	Submit: POST {base}/api/v1/services/aigc/text2image/image-synthesis
//	        header X-DashScope-Async: enable
//	        {model, input:{prompt}, parameters:{n,size}}  -> output.task_id
//	Poll:   GET  {base}/api/v1/tasks/{task_id}            -> output.task_status
//	Result: output.results[].url (download the image bytes, no auth header)
//
// NOTE: the exact paths/fields the yunwu proxy uses could not be verified online
// at authoring time; the shapes follow DashScope's public image-synthesis
// contract and are tolerant of several result-URL placements. Isolated here so
// they can be corrected against the live proxy without touching callers.
type DashScopeProvider struct {
	name    string
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
	// pollInterval is the gap between task polls; 0 means 5s. Tests set it small.
	pollInterval time.Duration
}

// NewDashScopeProvider builds a wan/qwen text-to-image provider from config.
func NewDashScopeProvider(cfg config.ImageProviderConfig) *DashScopeProvider {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://dashscope.aliyuncs.com"
	}
	return &DashScopeProvider{
		name:    cfg.Name,
		baseURL: base,
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// Name implements Provider.
func (p *DashScopeProvider) Name() string { return p.name }

// Generate implements Provider. It ignores any source/reference images: wan/qwen
// here are used as pure text-to-image backends.
func (p *DashScopeProvider) Generate(ctx context.Context, req Request) (Output, error) {
	if p.apiKey == "" {
		return Output{}, fmt.Errorf("provider %s: missing API key", p.name)
	}
	taskID, err := p.submit(ctx, req)
	if err != nil {
		return Output{}, err
	}
	url, err := p.poll(ctx, taskID)
	if err != nil {
		return Output{}, err
	}
	img, err := p.fetch(ctx, url)
	if err != nil {
		return Output{}, err
	}
	return Output{Data: img, Mime: "image/png"}, nil
}

func (p *DashScopeProvider) submit(ctx context.Context, req Request) (string, error) {
	params := map[string]any{"n": 1}
	if sz := sizeParam(req.Width, req.Height); sz != "" {
		params["size"] = strings.ReplaceAll(sz, "x", "*") // DashScope uses 1024*1024
	}
	body := map[string]any{
		"model":      p.model,
		"input":      map[string]any{"prompt": req.Prompt},
		"parameters": params,
	}
	buf, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/api/v1/services/aigc/text2image/image-synthesis", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("X-DashScope-Async", "enable")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("provider %s: submit: %w", p.name, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("provider %s: submit status %d: %s", p.name, resp.StatusCode, truncate(string(raw), 300))
	}
	var parsed struct {
		Output struct {
			TaskID string `json:"task_id"`
		} `json:"output"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("provider %s: decode submit: %w (body: %s)", p.name, err, truncate(string(raw), 200))
	}
	if parsed.Output.TaskID == "" {
		return "", fmt.Errorf("provider %s: no task_id (code=%s msg=%s)", p.name, parsed.Code, parsed.Message)
	}
	return parsed.Output.TaskID, nil
}

func (p *DashScopeProvider) poll(ctx context.Context, taskID string) (string, error) {
	interval := p.pollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("provider %s: task %s timed out", p.name, taskID)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/v1/tasks/"+taskID, nil)
		if err != nil {
			return "", err
		}
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
		resp, err := p.client.Do(httpReq)
		if err != nil {
			return "", fmt.Errorf("provider %s: poll: %w", p.name, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return "", fmt.Errorf("provider %s: poll status %d: %s", p.name, resp.StatusCode, truncate(string(raw), 300))
		}
		var parsed struct {
			Output struct {
				TaskStatus string `json:"task_status"`
				Results    []struct {
					URL string `json:"url"`
				} `json:"results"`
				Message string `json:"message"`
			} `json:"output"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return "", fmt.Errorf("provider %s: decode poll: %w (body: %s)", p.name, err, truncate(string(raw), 200))
		}
		switch strings.ToUpper(parsed.Output.TaskStatus) {
		case "SUCCEEDED":
			for _, r := range parsed.Output.Results {
				if r.URL != "" {
					return r.URL, nil
				}
			}
			return "", fmt.Errorf("provider %s: succeeded but no result url", p.name)
		case "FAILED", "CANCELED", "UNKNOWN":
			return "", fmt.Errorf("provider %s: task %s %s: %s", p.name, taskID, parsed.Output.TaskStatus, parsed.Output.Message)
		default:
			// PENDING / RUNNING: keep polling.
		}
	}
}

func (p *DashScopeProvider) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("provider %s: fetch result: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("provider %s: fetch result status %d", p.name, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
