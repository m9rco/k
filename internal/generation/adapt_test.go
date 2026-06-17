package generation

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gameasset/internal/crop"
	"gameasset/internal/store"
	"gameasset/internal/transport"
)

// --- helpers shared by adapt tests ---

// makePNG returns a minimal valid PNG of the given dimensions without needing
// testing.T — safe to call from mock implementations.
func makePNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func solidPNGAdapt(t *testing.T, w, h int) []byte {
	t.Helper()
	return makePNG(w, h)
}

func newAdaptService(t *testing.T) (*Service, *store.Store, string, *mockCropper) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	broker := transport.NewTaskBroker()
	var n int
	idFn := func(prefix string) string { n++; return prefix + strconv.Itoa(n) }
	// Use a stub provider that always produces a valid image.
	prov := &capturingProvider{
		name: "stubprov",
		out:  Output{Data: solidPNGAdapt(t, 400, 300), Mime: "image/png"},
	}
	svc := NewService(NewFailoverGenerator(prov, nil), st, broker, filepath.Join(dir, "assets"), idFn)
	mc := &mockCropper{st: st, dir: dir, idN: &n}
	svc.SetCropper(mc)

	now := time.Now().UTC()
	_ = st.UpsertSession(store.SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	srcPath := filepath.Join(dir, "src.png")
	_ = os.WriteFile(srcPath, solidPNGAdapt(t, 1920, 1080), 0o644)
	_ = st.InsertAsset(store.AssetRecord{
		ID: "src", SessionID: "s", Kind: "upload", Path: srcPath, Mime: "image/png",
		Width: 1920, Height: 1080, CreatedAt: now,
	})
	return svc, st, dir, mc
}

// mockCropper satisfies the Cropper interface for tests. It routes SizeSpec
// from a static catalog and performs real file-backed crop for the fast path.
type mockCropper struct {
	st    *store.Store
	dir   string
	idN   *int
	calls []string // SizeSpec ids called
}

var testCatalog = map[string]crop.SizeSpec{
	"same.landscape.1920x1080": {SizeID: "same.landscape.1920x1080", ChannelID: "ch", ChannelName: "TestCh", AssetTypeName: "Banner", Width: 1920, Height: 1080, Orientation: "landscape", Producible: true},
	"same.landscape.1280x720":  {SizeID: "same.landscape.1280x720", ChannelID: "ch", ChannelName: "TestCh", AssetTypeName: "Banner", Width: 1280, Height: 720, Orientation: "landscape", Producible: true},
	"flip.portrait.720x1280":   {SizeID: "flip.portrait.720x1280", ChannelID: "ch", ChannelName: "TestCh", AssetTypeName: "Story", Width: 720, Height: 1280, Orientation: "portrait", Producible: true},
	"flip.square.512x512":      {SizeID: "flip.square.512x512", ChannelID: "ch", ChannelName: "TestCh", AssetTypeName: "Icon", Width: 512, Height: 512, Orientation: "square", Producible: true},
	"nonprod.video":            {SizeID: "nonprod.video", ChannelID: "ch", ChannelName: "TestCh", AssetTypeName: "Video", Width: 1920, Height: 1080, Orientation: "landscape", Producible: false},
	// extreme aspect ratio: target ratio ≥ extremeConvergeRatio → deterministic
	// cover path (never outpaint), regardless of the gen product's ratio.
	"extreme.banner.1920x320": {SizeID: "extreme.banner.1920x320", ChannelID: "ch", ChannelName: "TestCh", AssetTypeName: "Banner", Width: 1920, Height: 320, Orientation: "landscape", Producible: true},
	// medium ratio gap (2:1 target vs 4:3 gen output): dstRatio < extreme threshold
	// but log-ratio gap >> 0.18 → triggers the outpaint convergence path.
	"medium.banner.1600x800": {SizeID: "medium.banner.1600x800", ChannelID: "ch", ChannelName: "TestCh", AssetTypeName: "Banner", Width: 1600, Height: 800, Orientation: "landscape", Producible: true},
	// same 4:3 ratio as gen output (400×300): never triggers outpaint (diff=0)
	"same.ratio.800x600": {SizeID: "same.ratio.800x600", ChannelID: "ch", ChannelName: "TestCh", AssetTypeName: "Banner", Width: 800, Height: 600, Orientation: "landscape", Producible: true},
}

func (m *mockCropper) SizeSpec(id string) (crop.SizeSpec, bool) {
	m.calls = append(m.calls, id)
	spec, ok := testCatalog[id]
	return spec, ok
}

func (m *mockCropper) CropToSizes(sessionID, sourceAssetID string, sizeIDs []string, lossless bool, opts crop.Options) ([]crop.CropResult, error) {
	now := time.Now().UTC()
	var out []crop.CropResult
	for _, id := range sizeIDs {
		spec := testCatalog[id]
		assetID := "cropped" + strconv.Itoa(*m.idN+100)
		*m.idN++
		path := filepath.Join(m.dir, assetID+".png")
		data := makePNG(spec.Width, spec.Height)
		_ = os.WriteFile(path, data, 0o644)
		_ = m.st.InsertAsset(store.AssetRecord{
			ID: assetID, SessionID: sessionID, Kind: "cropped", Path: path,
			Mime: "image/png", Width: spec.Width, Height: spec.Height,
			ParentID: sourceAssetID, CreatedAt: now,
		})
		out = append(out, crop.CropResult{AssetID: assetID, SizeID: id, ChannelID: spec.ChannelID, Width: spec.Width, Height: spec.Height})
	}
	return out, nil
}

// solidPNGAdapt overload that skips the testing.T helper for the mock.
func init() {}

// --- tests ---

func TestAdaptRatioMatchTakesCropPath(t *testing.T) {
	svc, _, _, _ := newAdaptService(t)
	// 1920×1080 source → 1280×720 target: same 16:9 ratio, crop fast path.
	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"same.landscape.1280x720"}, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("want 1 outcome, got %d", len(outcomes))
	}
	if outcomes[0].Via != AdaptViaCrop {
		t.Errorf("expected crop path, got via=%q", outcomes[0].Via)
	}
	if outcomes[0].AssetID == "" {
		t.Error("crop path must return assetID immediately")
	}
}

func TestRetryAssetReRunsFlowAsNewAsset(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	// Produce an AI-adapted asset first.
	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"flip.portrait.720x1280"}, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTask(t, st, "s", outcomes[0].TaskID)
	if rec.Status != "done" || rec.AssetID == "" {
		t.Fatalf("setup adapt failed: status=%q err=%q", rec.Status, rec.Error)
	}
	origID := rec.AssetID

	// Retry that successful product → a NEW task/asset, original untouched.
	newTaskID, err := svc.RetryAsset(context.Background(), "s", origID, nil)
	if err != nil {
		t.Fatalf("RetryAsset: %v", err)
	}
	newRec := waitTask(t, st, "s", newTaskID)
	if newRec.Status != "done" {
		t.Fatalf("retry task failed: %q", newRec.Error)
	}
	if newRec.AssetID == "" || newRec.AssetID == origID {
		t.Errorf("retry must yield a new asset id, got %q (orig %q)", newRec.AssetID, origID)
	}
	if orig, _ := st.GetAsset("s", origID); orig == nil {
		t.Error("original asset must be preserved after retry")
	}
}

func TestRetryAssetRejectsNonRetryable(t *testing.T) {
	svc, _, _, _ := newAdaptService(t)
	// "src" is a plain upload — no gen_origin, so not retryable.
	if _, err := svc.RetryAsset(context.Background(), "s", "src", nil); err == nil {
		t.Error("expected error retrying a non-AI (upload) asset")
	}
	if _, err := svc.RetryAsset(context.Background(), "s", "nope", nil); err == nil {
		t.Error("expected error retrying an unknown asset")
	}
}

func TestAdaptMultiRefForcesAIEvenOnRatioMatch(t *testing.T) {
	svc, st, dir, _ := newAdaptService(t)
	// Add a second reference asset (same 16:9 ratio as the target).
	now := time.Now().UTC()
	auxPath := filepath.Join(dir, "aux.png")
	_ = os.WriteFile(auxPath, solidPNGAdapt(t, 1920, 1080), 0o644)
	_ = st.InsertAsset(store.AssetRecord{
		ID: "aux", SessionID: "s", Kind: "upload", Path: auxPath, Mime: "image/png",
		Width: 1920, Height: 1080, CreatedAt: now,
	})
	// Anchor 1920×1080 → 1280×720 is a 16:9 ratio match (single image would crop),
	// but a 2-image reference group must force the AI repaint path so the auxiliary
	// reference isn't silently dropped.
	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src", "aux"}, []string{"same.landscape.1280x720"}, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("want 1 outcome, got %d", len(outcomes))
	}
	if outcomes[0].Via != AdaptViaAI {
		t.Errorf("reference group must force AI path, got via=%q", outcomes[0].Via)
	}
	rec := waitTask(t, st, "s", outcomes[0].TaskID)
	if rec.Status != "done" {
		t.Fatalf("AI task failed: %q", rec.Error)
	}
}

func TestAdaptMultiSizeProducesOnePerSize(t *testing.T) {
	svc, _, dir, _ := newAdaptService(t)
	now := time.Now().UTC()
	auxPath := filepath.Join(dir, "aux2.png")
	_ = os.WriteFile(auxPath, solidPNGAdapt(t, 1920, 1080), 0o644)
	_ = svc.store.InsertAsset(store.AssetRecord{
		ID: "aux2", SessionID: "s", Kind: "upload", Path: auxPath, Mime: "image/png",
		Width: 1920, Height: 1080, CreatedAt: now,
	})
	// Reference group of 2 + 3 target sizes → exactly 3 outcomes (one per size),
	// NOT references×sizes (which would be 6).
	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src", "aux2"},
		[]string{"same.landscape.1280x720", "flip.portrait.720x1280", "flip.square.512x512"}, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 3 {
		t.Fatalf("reference group over 3 sizes must yield 3 outcomes (one per size), got %d", len(outcomes))
	}
}

func TestAdaptOrientationFlipTakesAIPath(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	// 1920×1080 (landscape) → 720×1280 (portrait): orientation flip, AI path.
	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"flip.portrait.720x1280"}, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("want 1 outcome, got %d", len(outcomes))
	}
	if outcomes[0].Via != AdaptViaAI {
		t.Errorf("expected AI path, got via=%q", outcomes[0].Via)
	}
	if outcomes[0].TaskID == "" {
		t.Error("AI path must return a task ID")
	}
	rec := waitTask(t, st, "s", outcomes[0].TaskID)
	if rec.Status != "done" {
		t.Fatalf("AI task failed: %q", rec.Error)
	}
}

func TestAdaptSquareTakesAIPath(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	// 1920×1080 (landscape) → 512×512 (square): ratio change, AI path.
	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"flip.square.512x512"}, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].Via != AdaptViaAI {
		t.Errorf("expected AI path, got via=%q", outcomes[0].Via)
	}
	rec := waitTask(t, st, "s", outcomes[0].TaskID)
	if rec.Status != "done" {
		t.Fatalf("AI task not done: %q", rec.Error)
	}
	// Check that the AI product's meta carries sizeId + via=ai.
	asset, _ := st.GetAsset("s", rec.AssetID)
	if asset == nil || asset.Meta == "" {
		t.Fatal("adapt AI product missing meta")
	}
	if !containsStr(asset.Meta, `"sizeId":"flip.square.512x512"`) {
		t.Errorf("meta missing sizeId: %s", asset.Meta)
	}
	if !containsStr(asset.Meta, `"via":"ai"`) {
		t.Errorf("meta via!=ai: %s", asset.Meta)
	}
}

// TestAdaptReRequestRegenerates verifies that re-requesting the same (source,
// size) in the same session does NOT reuse the prior product but generates a
// fresh one. Session-level dedup was removed: a re-request means the user wants
// a new product (the prior dedup silently skipped generation, leaving the
// workspace seemingly empty — the bug this guards against).
func TestAdaptReRequestRegenerates(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	// First request: AI path (landscape→portrait flip).
	outcomes1, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"flip.portrait.720x1280"}, false, nil, "")
	if err != nil || outcomes1[0].Via != AdaptViaAI {
		t.Fatalf("first: want AI path, err=%v via=%s", err, outcomes1[0].Via)
	}
	rec1 := waitTask(t, st, "s", outcomes1[0].TaskID)

	// Second request (same session, same source, same size): must regenerate,
	// NOT reuse — a new AI task with a distinct product.
	outcomes2, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"flip.portrait.720x1280"}, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if outcomes2[0].Via != AdaptViaAI {
		t.Errorf("re-request must regenerate via AI, got via=%q", outcomes2[0].Via)
	}
	if outcomes2[0].TaskID == outcomes1[0].TaskID {
		t.Error("re-request reused the same task id; expected a fresh task")
	}
	rec2 := waitTask(t, st, "s", outcomes2[0].TaskID)
	if rec2.AssetID == rec1.AssetID {
		t.Error("re-request produced the same asset id; expected a fresh product")
	}
}

func TestAdaptUnknownSizeErrors(t *testing.T) {
	svc, _, _, _ := newAdaptService(t)
	_, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"doesnotexist"}, false, nil, "")
	if err == nil {
		t.Error("expected error for unknown size id")
	}
}

func TestAdaptNonProducibleSizeErrors(t *testing.T) {
	svc, _, _, _ := newAdaptService(t)
	_, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"nonprod.video"}, false, nil, "")
	if err == nil {
		t.Error("expected error for non-producible size")
	}
}

func TestAspectClose(t *testing.T) {
	cases := []struct {
		srcW, srcH, dstW, dstH int
		want                   bool
		label                  string
	}{
		{1920, 1080, 1280, 720, true, "16:9 same orientation"},
		{1920, 1080, 1920, 1080, true, "identical"},
		{1920, 1080, 720, 1280, false, "landscape→portrait flip"},
		{1920, 1080, 512, 512, false, "landscape→square"},
		{720, 1280, 1080, 1920, true, "portrait same ratio"},
		{0, 1080, 1280, 720, false, "zero srcW"},
		{1920, 0, 1280, 720, false, "zero srcH"},
	}
	for _, c := range cases {
		got := aspectClose(c.srcW, c.srcH, c.dstW, c.dstH)
		if got != c.want {
			t.Errorf("[%s] aspectClose(%d,%d,%d,%d) = %v, want %v", c.label, c.srcW, c.srcH, c.dstW, c.dstH, got, c.want)
		}
	}
}

func TestConvergeMode(t *testing.T) {
	cases := []struct {
		pin                    string
		genW, genH, dstW, dstH int
		want                   crop.Mode
		label                  string
	}{
		// Pins win outright regardless of ratio gap.
		{"contain", 1536, 1024, 1280, 320, crop.ModeContain, "pin contain on extreme target"},
		{"cover", 1024, 1024, 1024, 1024, crop.ModeCover, "pin cover on identical ratio"},
		{"outpaint", 1024, 1024, 1024, 1024, crop.ModeOutpaint, "pin outpaint wins outright"},
		{"scale", 1008, 336, 1008, 168, crop.ModeScale, "pin scale on extreme target wins over auto cover"},
		// Auto: close ratios rescale (scale).
		{"", 1536, 1024, 1280, 720, crop.ModeScale, "3:2 product → 16:9 target (close)"},
		{"", 1024, 1024, 1080, 1080, crop.ModeScale, "square → square"},
		// Auto: extreme TARGET ratio (≥3:1) → deterministic cover, never outpaint.
		{"", 1008, 336, 1008, 168, crop.ModeCover, "6:1 banner target → cover"},
		{"", 1008, 336, 1008, 202, crop.ModeCover, "5:1 banner target → cover"},
		{"", 1120, 373, 1120, 280, crop.ModeCover, "4:1 banner target → cover"},
		{"", 336, 1008, 168, 1008, crop.ModeCover, "1:6 vertical strip target → cover (symmetric)"},
		{"", 1024, 1024, 1280, 320, crop.ModeCover, "1:1 product → 4:1 extreme target → cover (not outpaint)"},
		// Auto: medium gap but target ratio < extreme threshold → outpaint.
		{"", 1024, 1024, 1280, 640, crop.ModeOutpaint, "1:1 product → 2:1 target (medium gap, legal ratio)"},
		// 临界: just under 3:1 with matching gen → scale (no extreme, gap tiny).
		{"", 1488, 512, 1440, 496, crop.ModeScale, "~2.9:1 matched gen → scale (below extreme)"},
		// Invalid dims fall back to scale.
		{"", 0, 1024, 1280, 720, crop.ModeScale, "zero genW"},
		{"", 1536, 1024, 1280, 0, crop.ModeScale, "zero dstH"},
		// Unknown pin string is ignored → auto (extreme target → cover).
		{"bogus", 1536, 1024, 1280, 320, crop.ModeCover, "unknown pin → auto cover on extreme target"},
	}
	for _, c := range cases {
		got := convergeMode(c.pin, c.genW, c.genH, c.dstW, c.dstH)
		if got != c.want {
			t.Errorf("[%s] convergeMode(%q,%d,%d,%d,%d) = %q, want %q",
				c.label, c.pin, c.genW, c.genH, c.dstW, c.dstH, got, c.want)
		}
	}
}

// TestResolveAndConvergeAgree verifies the D5 协同: the gpt-image-2 size resolver
// and the auto convergence mode agree on the right path per catalog size — a
// ratio-matched target downsamples by padding (contain), an extreme banner crops
// (cover). The product always converges to the EXACT catalog size (the crop layer
// guarantees the output dimensions), so this only asserts the mode selection.
func TestResolveAndConvergeAgree(t *testing.T) {
	cases := []struct {
		dstW, dstH int
		want       crop.Mode
		label      string
	}{
		{1280, 720, crop.ModeScale, "16:9 same-ratio → scale (downsample)"},
		{720, 1280, crop.ModeScale, "9:16 same-ratio → scale"},
		{512, 512, crop.ModeScale, "square icon → scale (downsample)"},
		{900, 600, crop.ModeScale, "3:2 cover → scale"},
		{1120, 280, crop.ModeCover, "4:1 banner (extreme target) → cover"},
		{1008, 168, crop.ModeCover, "6:1 strip (extreme target) → cover"},
		{2732, 2048, crop.ModeScale, "iOS 4:3 (>2K) → scale (upsample)"},
	}
	for _, c := range cases {
		size := resolveGptImage2Size(c.dstW, c.dstH)
		gw, gh := parseSize(t, size)
		got := convergeMode("", gw, gh, c.dstW, c.dstH)
		if got != c.want {
			t.Errorf("[%s] gen=%s converge=%q, want %q", c.label, size, got, c.want)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
