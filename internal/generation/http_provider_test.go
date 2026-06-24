package generation

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"strconv"
	"strings"
	"testing"

	"gameasset/internal/config"
)

// TestBuildEditRequestMultiImage verifies that a multi-image fusion edit sends
// EVERY input image (base first, then references) under the SAME repeated
// `image[]` multipart field. The previous code sent the base as a scalar `image`
// and references as `image[]`, which OpenAI-compatible gateways drop — so the
// model only saw the base and hallucinated the reference character.
func TestBuildEditRequestMultiImage(t *testing.T) {
	p := NewHTTPProvider(config.ImageProviderConfig{BaseURL: "https://example.test/v1", APIKey: "k", Model: "gpt-image-2"})
	base := []byte("BASE-IMAGE-BYTES")
	ref := []byte("REF-IMAGE-BYTES")

	httpReq, err := p.buildEditRequest(context.Background(), Request{
		Prompt:          "fuse",
		SourceImage:     base,
		ReferenceImages: [][]byte{ref},
		Width:           1024, Height: 1024,
	})
	if err != nil {
		t.Fatalf("buildEditRequest: %v", err)
	}

	_, params, err := mime.ParseMediaType(httpReq.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content-type: %v", err)
	}
	mr := multipart.NewReader(httpReq.Body, params["boundary"])

	var imageParts [][]byte
	var scalarImageSeen bool
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		data, _ := io.ReadAll(part)
		switch part.FormName() {
		case "image[]":
			imageParts = append(imageParts, data)
		case "image":
			scalarImageSeen = true
		}
	}

	if scalarImageSeen {
		t.Errorf("base image must NOT be sent as scalar `image` (gateways drop the array refs)")
	}
	if len(imageParts) != 2 {
		t.Fatalf("expected 2 image[] parts (base + ref), got %d", len(imageParts))
	}
	if string(imageParts[0]) != string(base) {
		t.Errorf("first image[] must be the base/source image; got %q", imageParts[0])
	}
	if string(imageParts[1]) != string(ref) {
		t.Errorf("second image[] must be the reference image; got %q", imageParts[1])
	}
}

// TestSizeParam verifies the LEGACY fixed-enum mapping (used by DashScope-style
// adapters) snaps to the nearest aspect ratio. gpt-image-2 does not use this — see
// TestResolveGptImage2Size.
func TestSizeParam(t *testing.T) {
	cases := []struct {
		w, h  int
		want  string
		label string
	}{
		{1024, 1024, "1024x1024", "square"},
		{1200, 800, "1536x1024", "3:2 landscape"},
		{800, 1200, "1024x1536", "2:3 portrait"},
		{1920, 1080, "1536x1024", "16:9 → nearest landscape"},
		{1080, 1920, "1024x1536", "9:16 → nearest portrait"},
		{2000, 500, "1536x1024", "4:1 extreme banner → widest legal"},
		{500, 2000, "1024x1536", "1:4 extreme tall → tallest legal"},
		{1040, 1024, "1024x1024", "near-square stays square"},
		{0, 1024, "", "zero width → provider decides"},
		{1024, 0, "", "zero height → provider decides"},
	}
	for _, c := range cases {
		if got := sizeParam(c.w, c.h); got != c.want {
			t.Errorf("[%s] sizeParam(%d,%d) = %q, want %q", c.label, c.w, c.h, got, c.want)
		}
	}
}

// parseSize splits a "WxH" gpt-image-2 size label into ints.
func parseSize(t *testing.T, s string) (int, int) {
	t.Helper()
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		t.Fatalf("malformed size %q", s)
	}
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		t.Fatalf("non-numeric size %q", s)
	}
	return w, h
}

// assertLegal checks a resolver output against every gpt-image-2 constraint.
func assertLegal(t *testing.T, label string, w, h int) {
	t.Helper()
	if w <= 0 || h <= 0 {
		t.Errorf("[%s] non-positive %dx%d", label, w, h)
		return
	}
	if w%gptImage2SizeMultiple != 0 || h%gptImage2SizeMultiple != 0 {
		t.Errorf("[%s] %dx%d not multiples of %d", label, w, h, gptImage2SizeMultiple)
	}
	if w > gptImage2MaxEdge || h > gptImage2MaxEdge {
		t.Errorf("[%s] %dx%d exceeds max edge %d", label, w, h, gptImage2MaxEdge)
	}
	px := w * h
	if px < gptImage2MinPixels || px > gptImage2MaxPixels {
		t.Errorf("[%s] %dx%d total px %d out of [%d,%d]", label, w, h, px, gptImage2MinPixels, gptImage2MaxPixels)
	}
	lo, hi := w, h
	if lo > hi {
		lo, hi = hi, lo
	}
	if float64(hi)/float64(lo) > gptImage2MaxRatio+1e-9 {
		t.Errorf("[%s] %dx%d long:short ratio exceeds %.0f:1", label, w, h, gptImage2MaxRatio)
	}
}

// TestResolveGptImage2Size verifies the resolver matches target orientation/ratio
// within constraints and snaps extreme ratios to the ≤3:1 band.
func TestResolveGptImage2Size(t *testing.T) {
	cases := []struct {
		w, h  int
		label string
	}{
		{1280, 720, "16:9 landscape"},
		{720, 1280, "9:16 portrait"},
		{512, 512, "square icon (below floor)"},
		{900, 600, "3:2 cover (below floor)"},
		{1120, 280, "4:1 banner (extreme)"},
		{1008, 168, "6:1 strip (extreme)"},
		{2732, 2048, "iOS 4:3 (above 2K)"},
		{150, 150, "tiny icon"},
	}
	for _, c := range cases {
		got := resolveGptImage2Size(c.w, c.h)
		if got == "" {
			t.Errorf("[%s] resolveGptImage2Size(%d,%d) empty", c.label, c.w, c.h)
			continue
		}
		gw, gh := parseSize(t, got)
		assertLegal(t, c.label, gw, gh)
		// Orientation must match the target (extreme ratios stay same orientation,
		// just clamped to 3:1).
		if orientationOf(gw, gh) != orientationOf(c.w, c.h) {
			t.Errorf("[%s] orientation %s != target %s (%s)", c.label, orientationOf(gw, gh), orientationOf(c.w, c.h), got)
		}
	}
	// Zero dimensions → provider decides (empty).
	if resolveGptImage2Size(0, 1024) != "" || resolveGptImage2Size(1024, 0) != "" {
		t.Error("zero dimension should resolve to empty")
	}
}

// TestResolveGptImage2SizeBudgetCap asserts large targets (already ≥ the legal
// minimum) generate near their own pixel count instead of being gratuitously
// upscaled to the ~3MP quality budget (the latency fix), while sub-minimum targets
// still upscale toward the budget for a crisp downsample.
func TestResolveGptImage2SizeBudgetCap(t *testing.T) {
	pixels := func(w, h int) int { return w * h }

	// Large, balanced targets: gen pixels should stay close to the target's own
	// count (within ~10%), NOT balloon toward 3MP.
	large := []struct {
		w, h  int
		label string
	}{
		{2080, 828, "TapTap search banner (~1.72MP)"},
		{1920, 1080, "16:9 welfare banner (~2.07MP)"},
	}
	for _, c := range large {
		gw, gh := parseSize(t, resolveGptImage2Size(c.w, c.h))
		assertLegal(t, c.label, gw, gh)
		tp := pixels(c.w, c.h)
		gp := pixels(gw, gh)
		if gp > tp*110/100 {
			t.Errorf("[%s] gen %dx%d (%dMP) over-upscaled target %dx%d (%dMP); want ≈ target, not ~3MP",
				c.label, gw, gh, gp/1_000_000, c.w, c.h, tp/1_000_000)
		}
	}

	// Sub-minimum targets: must still be upscaled toward the budget (well above the
	// target's own pixel count) so the downsample stays sharp.
	small := []struct {
		w, h  int
		label string
	}{
		{512, 512, "icon (0.26MP)"},
		{900, 600, "small cover (0.54MP)"},
	}
	for _, c := range small {
		gw, gh := parseSize(t, resolveGptImage2Size(c.w, c.h))
		assertLegal(t, c.label, gw, gh)
		if pixels(gw, gh) <= pixels(c.w, c.h) {
			t.Errorf("[%s] gen %dx%d not upscaled above target %dx%d; small-target sharpening lost",
				c.label, gw, gh, c.w, c.h)
		}
	}
}

// TestResolveGptImage2SizeCoversCatalog asserts the resolver produces a legal
// gpt-image-2 size for EVERY producible catalog entry — no size in channels.json
// can drive the resolver outside the model's constraints (design task 1.4).
func TestResolveGptImage2SizeCoversCatalog(t *testing.T) {
	raw, err := os.ReadFile("../../configs/channels.json")
	if err != nil {
		t.Fatalf("read channels.json: %v", err)
	}
	var cat struct {
		Channels []struct {
			AssetTypes []struct {
				Sizes []struct {
					ID         string `json:"id"`
					Width      int    `json:"width"`
					Height     int    `json:"height"`
					Producible bool   `json:"producible"`
				} `json:"sizes"`
			} `json:"assetTypes"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		t.Fatalf("parse channels.json: %v", err)
	}
	n := 0
	for _, ch := range cat.Channels {
		for _, at := range ch.AssetTypes {
			for _, s := range at.Sizes {
				if !s.Producible {
					continue
				}
				n++
				got := resolveGptImage2Size(s.Width, s.Height)
				if got == "" {
					t.Errorf("size %s (%dx%d) resolved empty", s.ID, s.Width, s.Height)
					continue
				}
				gw, gh := parseSize(t, got)
				assertLegal(t, s.ID, gw, gh)
			}
		}
	}
	if n == 0 {
		t.Fatal("no producible sizes found — catalog path wrong?")
	}
	t.Logf("verified %d producible catalog sizes", n)
}
