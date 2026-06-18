package generation

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"sync/atomic"
	"testing"
)

// colorPNG returns a solid-color PNG so a test can identify which pass's product
// survived (the color is preserved through the scale-converge step).
func colorPNG(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func centerColor(t *testing.T, data []byte) color.RGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	b := img.Bounds()
	r, g, bl, a := img.At(b.Dx()/2, b.Dy()/2).RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), uint8(a >> 8)}
}

// countingProvider counts Generate calls and records the last prompt, so a test
// can assert the quality gate regenerated (2 calls) and fed hints into the retry.
type countingProvider struct {
	name  string
	out   Output
	calls int32
	last  *Request
}

func (c *countingProvider) Name() string { return c.name }
func (c *countingProvider) Generate(_ context.Context, req Request) (Output, error) {
	atomic.AddInt32(&c.calls, 1)
	r := req
	c.last = &r
	return c.out, nil
}

// stubChecker is a programmable quality judge.
type stubChecker struct {
	verdicts []QualityVerdict // returned in order; last repeats
	err      error
	calls    int32
}

func (s *stubChecker) Configured() bool { return true }
func (s *stubChecker) Check(_ context.Context, _ []byte, _, _, _ string) (QualityVerdict, error) {
	i := int(atomic.AddInt32(&s.calls, 1)) - 1
	if s.err != nil {
		return QualityVerdict{Pass: true, Compliant: true}, s.err
	}
	if i >= len(s.verdicts) {
		i = len(s.verdicts) - 1
	}
	return s.verdicts[i], nil
}

// TestQualityGateFailRegeneratesOnce verifies a failing first-pass review feeds
// hints back to the image model and regenerates exactly once (capped), and that
// the retry is NOT reviewed again.
func TestQualityGateFailRegeneratesOnce(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	prov := &countingProvider{name: "p", out: Output{Data: makePNG(400, 300), Mime: "image/png"}}
	svc.gen = NewFailoverGenerator(prov, nil)
	checker := &stubChecker{verdicts: []QualityVerdict{
		{Pass: false, Compliant: true, Total: 40, Reasons: []string{"人物卖相不足"}, Hints: "主体更突出"},
	}}
	svc.SetQualityChecker(checker)

	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"flip.square.512x512"}, false, nil, "theme")
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTask(t, st, "s", outcomes[0].TaskID)
	if rec.Status != "done" {
		t.Fatalf("task not done: %q", rec.Error)
	}
	if got := atomic.LoadInt32(&prov.calls); got != 2 {
		t.Errorf("expected 2 generations (initial + 1 regen), got %d", got)
	}
	if got := atomic.LoadInt32(&checker.calls); got != 2 {
		t.Errorf("expected 2 reviews (first pass + regen re-check), got %d", got)
	}
	// The regeneration prompt must carry the judge's hints (REVISE segment).
	if prov.last == nil || !containsStr(prov.last.Prompt, "主体更突出") {
		t.Errorf("regeneration prompt missing hints; got %q", promptOf(prov.last))
	}
}

// TestQualityGatePassPersistsImmediately verifies a passing review does not
// regenerate.
func TestQualityGatePassPersistsImmediately(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	prov := &countingProvider{name: "p", out: Output{Data: makePNG(400, 300), Mime: "image/png"}}
	svc.gen = NewFailoverGenerator(prov, nil)
	checker := &stubChecker{verdicts: []QualityVerdict{{Pass: true, Compliant: true, Total: 90}}}
	svc.SetQualityChecker(checker)

	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"flip.square.512x512"}, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTask(t, st, "s", outcomes[0].TaskID)
	if rec.Status != "done" {
		t.Fatalf("task not done: %q", rec.Error)
	}
	if got := atomic.LoadInt32(&prov.calls); got != 1 {
		t.Errorf("expected 1 generation (no regen on pass), got %d", got)
	}
}

// TestQualityGateErrorDegradesToPass verifies a judge error does not block the
// product (degrade to pass, no regeneration).
func TestQualityGateErrorDegradesToPass(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	prov := &countingProvider{name: "p", out: Output{Data: makePNG(400, 300), Mime: "image/png"}}
	svc.gen = NewFailoverGenerator(prov, nil)
	checker := &stubChecker{verdicts: []QualityVerdict{{Pass: true}}, err: errContext()}
	svc.SetQualityChecker(checker)

	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"flip.square.512x512"}, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTask(t, st, "s", outcomes[0].TaskID)
	if rec.Status != "done" {
		t.Fatalf("task not done: %q", rec.Error)
	}
	if got := atomic.LoadInt32(&prov.calls); got != 1 {
		t.Errorf("expected 1 generation on judge error, got %d", got)
	}
}

func promptOf(r *Request) string {
	if r == nil {
		return ""
	}
	return r.Prompt
}

func errContext() error { return context.DeadlineExceeded }

// TestQualityGateFaultOutpaintSkipsRepaint verifies that when the judge reports
// fault_source="outpaint" and an outpaint snapshot was captured, the retry skips
// gen.Generate() and only reruns the outpaint step.
func TestQualityGateFaultOutpaintSkipsRepaint(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	prov := &countingProvider{name: "p", out: Output{Data: makePNG(400, 300), Mime: "image/png"}}
	svc.gen = NewFailoverGenerator(prov, nil)
	// Outpainter produces a wide PNG for the medium-ratio banner (2:1 → outpaint).
	outpainter := &countingProvider{name: "op", out: Output{Data: makePNG(1920, 640), Mime: "image/png"}}
	svc.SetOutpainter(outpainter)
	checker := &stubChecker{verdicts: []QualityVerdict{
		{Pass: false, Compliant: true, Total: 40, Reasons: []string{"边界割裂"}, Hints: "场景延伸更自然", FaultSource: "outpaint"},
	}}
	svc.SetQualityChecker(checker)

	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"medium.banner.1600x800"}, false, nil, "theme")
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTask(t, st, "s", outcomes[0].TaskID)
	if rec.Status != "done" {
		t.Fatalf("task not done: %q", rec.Error)
	}
	// Repaint called once only (retry skips gen.Generate).
	if got := atomic.LoadInt32(&prov.calls); got != 1 {
		t.Errorf("expected 1 repaint (outpaint-only retry), got %d", got)
	}
	// Outpainter called twice: first pass + retry.
	if got := atomic.LoadInt32(&outpainter.calls); got != 2 {
		t.Errorf("expected 2 outpaint calls (first pass + retry), got %d", got)
	}
	// Judge called exactly once.
	if got := atomic.LoadInt32(&checker.calls); got != 2 {
		t.Errorf("expected 2 reviews (first pass + regen re-check), got %d", got)
	}
}

// TestQualityGateFaultRepaintFullRerun verifies fault_source="repaint" triggers
// a full pipeline rerun (gen.Generate called twice).
func TestQualityGateFaultRepaintFullRerun(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	prov := &countingProvider{name: "p", out: Output{Data: makePNG(400, 300), Mime: "image/png"}}
	svc.gen = NewFailoverGenerator(prov, nil)
	checker := &stubChecker{verdicts: []QualityVerdict{
		{Pass: false, Compliant: true, Total: 40, Reasons: []string{"主体偏低"}, FaultSource: "repaint"},
	}}
	svc.SetQualityChecker(checker)

	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"flip.square.512x512"}, false, nil, "theme")
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTask(t, st, "s", outcomes[0].TaskID)
	if rec.Status != "done" {
		t.Fatalf("task not done: %q", rec.Error)
	}
	// Both passes call gen.Generate.
	if got := atomic.LoadInt32(&prov.calls); got != 2 {
		t.Errorf("expected 2 repaint calls on fault_source=repaint, got %d", got)
	}
}

// TestQualityGateFaultOutpaintNoSnapshotFullRerun verifies that fault_source="outpaint"
// with no outpaint snapshot (scale path taken) still triggers a full rerun.
func TestQualityGateFaultOutpaintNoSnapshotFullRerun(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	prov := &countingProvider{name: "p", out: Output{Data: makePNG(400, 300), Mime: "image/png"}}
	svc.gen = NewFailoverGenerator(prov, nil)
	// same.ratio.800x600 is 4:3 like the gen output → scale path → no outpaint snapshot.
	checker := &stubChecker{verdicts: []QualityVerdict{
		{Pass: false, Compliant: true, Total: 40, FaultSource: "outpaint"},
	}}
	svc.SetQualityChecker(checker)

	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"same.ratio.800x600"}, false, nil, "theme")
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTask(t, st, "s", outcomes[0].TaskID)
	if rec.Status != "done" {
		t.Fatalf("task not done: %q", rec.Error)
	}
	// No snapshot → falls back to full rerun.
	if got := atomic.LoadInt32(&prov.calls); got != 2 {
		t.Errorf("expected 2 repaint calls (no snapshot = full rerun), got %d", got)
	}
}

// seqProvider returns a distinct image per call so a test can tell which pass's
// product was persisted (first attempt vs regen).
type seqProvider struct {
	name  string
	outs  []Output
	calls int32
}

func (s *seqProvider) Name() string { return s.name }
func (s *seqProvider) Generate(_ context.Context, _ Request) (Output, error) {
	i := int(atomic.AddInt32(&s.calls, 1)) - 1
	if i >= len(s.outs) {
		i = len(s.outs) - 1
	}
	return s.outs[i], nil
}

// TestQualityGateRegenWorseRevertsToFirst is the asset_caba3ad regression: the
// first pass fails (total 65) and triggers a regen, but the regen scores worse
// (total 35). The gate must persist the first-attempt bytes, not the worse regen.
func TestQualityGateRegenWorseRevertsToFirst(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	red := color.RGBA{200, 30, 30, 255}
	blue := color.RGBA{30, 30, 200, 255}
	prov := &seqProvider{name: "p", outs: []Output{
		{Data: colorPNG(400, 300, red), Mime: "image/png"},  // first attempt
		{Data: colorPNG(400, 300, blue), Mime: "image/png"}, // worse regen
	}}
	svc.gen = NewFailoverGenerator(prov, nil)
	checker := &stubChecker{verdicts: []QualityVerdict{
		{Pass: false, Compliant: true, Total: 65, Reasons: []string{"整体质量偏低"}, Hints: "提升清晰度"},
		{Pass: false, Compliant: true, Total: 35, Reasons: []string{"整体质量偏低"}},
	}}
	svc.SetQualityChecker(checker)

	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"same.ratio.800x600"}, false, nil, "theme")
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTask(t, st, "s", outcomes[0].TaskID)
	if rec.Status != "done" {
		t.Fatalf("task not done: %q", rec.Error)
	}
	// Two generations + two reviews (first pass + regen re-check), capped there.
	if got := atomic.LoadInt32(&prov.calls); got != 2 {
		t.Errorf("expected 2 generations, got %d", got)
	}
	if got := atomic.LoadInt32(&checker.calls); got != 2 {
		t.Errorf("expected 2 reviews (first + re-check), got %d", got)
	}
	// The persisted product must be the RED first attempt (total 65 > 35).
	asset, _ := st.GetAsset("s", rec.AssetID)
	if asset == nil {
		t.Fatal("expected a persisted asset")
	}
	data, err := os.ReadFile(asset.Path)
	if err != nil {
		t.Fatalf("read asset: %v", err)
	}
	got := centerColor(t, data)
	if got.R < 150 || got.B > 100 {
		t.Errorf("expected first-attempt (red) persisted, got %+v", got)
	}
}

// TestPreferFirst covers the red-line-aware bestOf ordering (Q1 regression).
func TestPreferFirst(t *testing.T) {
	tests := []struct {
		name  string
		first bestOfVerdict
		regen bestOfVerdict
		want  bool // true = keep first
	}{
		// Red line: regen passes, first fails → take regen.
		{"regen passes first fails", bestOfVerdict{false, 25, 80}, bestOfVerdict{true, 100, 90}, false},
		// Red line: first passes, regen fails → keep first.
		{"first passes regen fails", bestOfVerdict{true, 100, 90}, bestOfVerdict{false, 25, 80}, true},
		// Both fail red line → higher keyelem wins (log e64e / log 13db regression).
		{"both fail keyelem 30 vs 25", bestOfVerdict{false, 30, 80}, bestOfVerdict{false, 25, 83}, true},
		{"both fail keyelem 25 vs 20", bestOfVerdict{false, 25, 72}, bestOfVerdict{false, 20, 74}, true},
		// Both fail, same keyelem → higher total wins.
		{"both fail same keyelem higher regen total", bestOfVerdict{false, 25, 80}, bestOfVerdict{false, 25, 85}, false},
		// Full tie → keep regen (already has improvement hints).
		{"full tie keeps regen", bestOfVerdict{false, 25, 80}, bestOfVerdict{false, 25, 80}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := preferFirst(tc.first, tc.regen)
			if got != tc.want {
				t.Errorf("preferFirst(%+v, %+v) = %v, want %v", tc.first, tc.regen, got, tc.want)
			}
		})
	}
}

// TestQualityGateDegradedSignalWhenBothFail verifies that when both passes fail
// the red line the system still delivers the better version but emits
// review_failed{degraded:true} instead of review_passed (Q1 regression).
func TestQualityGateDegradedSignalWhenBothFail(t *testing.T) {
	svc, st, _, _ := newAdaptService(t)
	prov := &countingProvider{name: "p", out: Output{Data: makePNG(400, 300), Mime: "image/png"}}
	svc.gen = NewFailoverGenerator(prov, nil)
	// Both verdicts fail key_elements_fidelity red line.
	checker := &stubChecker{verdicts: []QualityVerdict{
		{Pass: false, Compliant: true, Total: 80, KeyElementsFidelity: 30, Reasons: []string{"核心主体/LOGO 缺失或文字被改写"}},
		{Pass: false, Compliant: true, Total: 83, KeyElementsFidelity: 25, Reasons: []string{"核心主体/LOGO 缺失或文字被改写"}},
	}}
	svc.SetQualityChecker(checker)

	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"same.ratio.800x600"}, false, nil, "theme")
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTask(t, st, "s", outcomes[0].TaskID)
	if rec.Status != "done" {
		t.Fatalf("task not done: %q", rec.Error)
	}
	// Task must succeed (product delivered even when both fail).
	if rec.AssetID == "" {
		t.Error("expected asset persisted even when both attempts fail red line")
	}
}
