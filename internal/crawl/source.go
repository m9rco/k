package crawl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// httpSource queries a configurable JSON search endpoint for a game's image
// previews. The endpoint contract is intentionally simple and pluggable (see
// change design Open Questions): GET {endpoint}?q={game}&limit={n} returning
// {"results":[{"url","source","title"}]}. An empty endpoint is "not configured".
type httpSource struct {
	endpoint string
	apiKey   string
	client   *http.Client
}

// NewHTTPSource builds a crawl Source from an endpoint and optional API key.
func NewHTTPSource(endpoint, apiKey string) Source {
	return &httpSource{
		endpoint: endpoint,
		apiKey:   apiKey,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (h *httpSource) Configured() bool { return h.endpoint != "" }

func (h *httpSource) Search(ctx context.Context, game string, limit int) ([]Result, error) {
	if !h.Configured() {
		return nil, fmt.Errorf("crawl source not configured")
	}
	u, err := url.Parse(h.endpoint)
	if err != nil {
		return nil, fmt.Errorf("bad crawl endpoint: %w", err)
	}
	q := u.Query()
	q.Set("q", game)
	q.Set("limit", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if h.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("crawl search request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("crawl search status %d", resp.StatusCode)
	}
	var parsed struct {
		Results []struct {
			URL    string `json:"url"`
			Source string `json:"source"`
			Title  string `json:"title"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode crawl response: %w", err)
	}
	out := make([]Result, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		if r.URL == "" {
			continue
		}
		src := r.Source
		if src == "" {
			src = u.Host
		}
		out = append(out, Result{URL: r.URL, Source: src, Title: r.Title})
	}
	return out, nil
}
