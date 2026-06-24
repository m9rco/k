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
		client:  &http.Client{Timeout: 5 * time.Minute},
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
	if sz := resolveGptImage2Size(req.Width, req.Height); sz != "" {
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
	if sz := resolveGptImage2Size(req.Width, req.Height); sz != "" {
		_ = mw.WriteField("size", sz)
	}
	// NOTE: input_fidelity is intentionally NOT sent. gpt-image-2 processes every
	// input image at high fidelity automatically and rejects the parameter; this
	// auto-fidelity is the harness's main lever against subject drift (design D4).
	// All input images go under the SAME repeated multipart field `image[]`, in
	// order: the base/source image FIRST, then any additional references. This is
	// the gpt-image multi-image edit convention (the SDK's `image=[...]` array
	// encodes to repeated `image[]` parts) — and the FIRST image is the one a mask
	// would apply to, matching our "source is the base, references are fused in"
	// semantics. Sending the base as a scalar `image` and references as `image[]`
	// makes OpenAI-compatible gateways drop the `image[]` parts, so the model only
	// ever sees the base and hallucinates the reference character (the fusion bug).
	imgParts := make([][]byte, 0, 1+len(req.ReferenceImages))
	imgParts = append(imgParts, req.SourceImage)
	imgParts = append(imgParts, req.ReferenceImages...)
	for i, img := range imgParts {
		if len(img) == 0 {
			continue
		}
		fw, err := mw.CreateFormFile("image[]", fmt.Sprintf("image%d.png", i+1))
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write(img); err != nil {
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

// genSize is one of the legacy fixed-enum output dimensions, with its precomputed
// aspect ratio. Used by the fixed-enum adapters (DashScope wan/qwen): any other
// requested size is snapped to the nearest legal default. gpt-image-2 does NOT use
// this — it accepts near-arbitrary sizes via resolveGptImage2Size below.
type genSize struct {
	label string
	ratio float64 // width/height
}

// legalGenSizes is the legacy fixed-size enum (square, landscape 3:2, portrait
// 2:3) used by DashScope-style adapters. Kept as data so the mapping is
// centralized, pre-configurable, and unit-testable. gpt-image-2's resolver is
// separate (resolveGptImage2Size).
var legalGenSizes = []genSize{
	{label: "1024x1024", ratio: 1.0},             // 1:1
	{label: "1536x1024", ratio: 1536.0 / 1024.0}, // 3:2 landscape
	{label: "1024x1536", ratio: 1024.0 / 1536.0}, // 2:3 portrait
}

// sizeParam maps requested dimensions to the legacy fixed enum by NEAREST ASPECT
// RATIO (log-distance, so landscape/portrait are symmetric) rather than a coarse
// orientation三分类. Used by fixed-enum adapters (DashScope). Empty means "let the
// provider decide". gpt-image-2 uses resolveGptImage2Size instead.
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

// gpt-image-2 size constraints (OpenAI image-generation docs, 2026): longest edge
// ≤3840, both edges multiples of 16, long:short ratio ≤3:1, total pixels within
// [655360, 8294400]; >3686400px (>2K) is experimental.
const (
	gptImage2MaxEdge      = 3840
	gptImage2MinPixels    = 655360
	gptImage2MaxPixels    = 8294400
	gptImage2MaxRatio     = 3.0
	gptImage2SizeMultiple = 16
	// gptImage2ExperimentalPixels is the boundary (1920×1920) above which output
	// is "experimental" per the docs; we keep generation at or below it.
	gptImage2ExperimentalPixels = 3686400
	// gptImage2GenBudget targets ~3MP: close to but under the 2K experimental
	// boundary (3686400px) for quality headroom, with margin for 16px rounding.
	// We always generate at this budget — catalog sizes below it (57% of the
	// catalog) downsample to exact afterward (sharper small images), and the few
	// >2K sizes (iOS 2732×2048) upsample to exact — avoiding the experimental
	// tier by default (design D1/D5).
	gptImage2GenBudget = 3_000_000.0
)

// resolveGptImage2Size returns the gpt-image-2 `size` value: a legal generation
// size that matches the target aspect ratio (clamped to ≤3:1 for extreme banners)
// at a ~3MP budget. The exact catalog size is produced by the downstream
// convergence step, not here — this only fixes the generation proportions so that
// convergence is a clean rescale/crop rather than a blind reshape (design D1).
// Returns "" when either dimension is 0 (source-less generation → provider auto).
func resolveGptImage2Size(dstW, dstH int) string {
	if dstW <= 0 || dstH <= 0 {
		return ""
	}
	// Generation aspect ratio = target ratio, clamped into the ≤3:1 band. Extreme
	// banners (4:1, 6:1) generate at 3:1 and are cover-cropped to exact later.
	ar := float64(dstW) / float64(dstH) // w/h
	clamped := false
	if maxAR := gptImage2MaxRatio; ar > maxAR {
		ar = maxAR
		clamped = true
	} else if minAR := 1.0 / gptImage2MaxRatio; ar < minAR {
		ar = minAR
		clamped = true
	}
	// Generation pixel budget. Quality-first by default, but DON'T gratuitously
	// upscale a target that is already at/above gpt-image-2's legal minimum: that
	// only inflates latency (a ~1.7MP banner generated at the 3MP budget measured
	// ~166s) for no real sharpness gain, since the source is already high-res.
	// Sub-minimum targets (icons/small covers) keep the full budget so their
	// downsample stays crisp. The 3MP ceiling is retained, so a >2K target (iOS
	// 2732×2048) is still clamped to ~2K and upsampled during convergence. The cap
	// is skipped for ratio-clamped (extreme) targets: their gen ratio differs from
	// the target, so the target pixel count is not a meaningful budget, and a
	// smaller budget leaves too little headroom for the 16-grid rounding to stay
	// within the 3:1 band — they cover-crop to exact later and aren't the latency
	// concern anyway.
	budget := gptImage2GenBudget
	if !clamped {
		if tp := float64(dstW) * float64(dstH); tp >= gptImage2MinPixels && tp < budget {
			budget = tp
		}
	}
	// Solve W,H from ratio + pixel budget: w = ar·h, w·h = budget → h = √(budget/ar).
	h := math.Sqrt(budget / ar)
	w := ar * h
	wi := roundTo16(w)
	hi := roundTo16(h)
	// Clamp the longest edge ≤ max, scaling both to keep the ratio, then re-round.
	if longest := maxInt(wi, hi); longest > gptImage2MaxEdge {
		scale := float64(gptImage2MaxEdge) / float64(longest)
		wi = roundTo16(float64(wi) * scale)
		hi = roundTo16(float64(hi) * scale)
	}
	// Floor each edge to one grid step so neither collapses to 0.
	wi = maxInt(wi, gptImage2SizeMultiple)
	hi = maxInt(hi, gptImage2SizeMultiple)
	// Belt-and-suspenders pixel-floor guard (a 3MP budget never trips it, but the
	// contract is that the output always satisfies gpt-image-2's [min,max] range).
	for wi*hi < gptImage2MinPixels {
		wi += gptImage2SizeMultiple
		hi += gptImage2SizeMultiple
	}
	return fmt.Sprintf("%dx%d", wi, hi)
}

// roundTo16 rounds v to the nearest multiple of 16 (gpt-image-2's size grid).
func roundTo16(v float64) int {
	return int(math.Round(v/gptImage2SizeMultiple)) * gptImage2SizeMultiple
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
