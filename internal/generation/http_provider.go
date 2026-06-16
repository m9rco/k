package generation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
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
	body, readErr := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return Output{}, fmt.Errorf("provider %s: status %d: %s", p.name, resp.StatusCode, truncate(string(body), 300))
	}
	if readErr != nil {
		return Output{}, fmt.Errorf("provider %s: read response body: %w", p.name, readErr)
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
	// Additional references (multi-image edit). Sent as image[] parts following
	// the gpt-image multi-image convention; providers that don't support it
	// ignore the extra parts and operate on the primary image alone.
	for i, ref := range req.ReferenceImages {
		if len(ref) == 0 {
			continue
		}
		rw, err := mw.CreateFormFile("image[]", fmt.Sprintf("ref%d.png", i+1))
		if err != nil {
			return nil, err
		}
		if _, err := rw.Write(ref); err != nil {
			return nil, err
		}
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

// genSize is one of the image API's legal output dimensions, with its precomputed
// aspect ratio. gpt-image-2 only accepts this fixed enum: any other requested size
// is snapped to the model's nearest default. We therefore pick the legal size whose
// aspect ratio is closest to the target, so the generated composition is as close to
// the target proportions as possible before the convergence step trims it to exact.
type genSize struct {
	label string
	ratio float64 // width/height
}

// legalGenSizes is the gpt-image-2 supported size enum (square, landscape 3:2,
// portrait 2:3). Kept as data so the mapping is centralized, pre-configurable, and
// unit-testable.
var legalGenSizes = []genSize{
	{label: "1024x1024", ratio: 1.0},             // 1:1
	{label: "1536x1024", ratio: 1536.0 / 1024.0}, // 3:2 landscape
	{label: "1024x1536", ratio: 1024.0 / 1536.0}, // 2:3 portrait
}

// sizeParam maps requested dimensions to the gpt-image-2 size enum by NEAREST
// ASPECT RATIO (log-distance, so landscape/portrait are symmetric) rather than a
// coarse orientation三分类 — this keeps 3:2 and 4:1 targets on the closest legal
// proportion instead of collapsing both to the same landscape value. Empty means
// "let the provider decide". We never pass "auto": auto lets the model pick the
// size, making the output ratio unpredictable and the convergence step unable to
// estimate padding/crop; an explicit legal enum keeps it deterministic.
func sizeParam(w, h int) string {
	if w == 0 || h == 0 {
		return ""
	}
	return nearestGenSize(w, h)
}

// nearestGenSize returns the legal gen-size label whose aspect ratio is closest to
// w×h, measured by absolute log-ratio distance (symmetric across orientation).
func nearestGenSize(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	target := math.Log(float64(w) / float64(h))
	best := legalGenSizes[0]
	bestDist := math.Abs(target - math.Log(best.ratio))
	for _, c := range legalGenSizes[1:] {
		if d := math.Abs(target - math.Log(c.ratio)); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best.label
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
