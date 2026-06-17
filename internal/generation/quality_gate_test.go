package generation

import (
	"context"
	"sync/atomic"
	"testing"
)

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
	if got := atomic.LoadInt32(&checker.calls); got != 1 {
		t.Errorf("expected exactly 1 review (retry not re-reviewed), got %d", got)
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
	// Outpainter produces a wide PNG for the extreme banner size.
	outpainter := &countingProvider{name: "op", out: Output{Data: makePNG(1920, 640), Mime: "image/png"}}
	svc.SetOutpainter(outpainter)
	checker := &stubChecker{verdicts: []QualityVerdict{
		{Pass: false, Compliant: true, Total: 40, Reasons: []string{"边界割裂"}, Hints: "场景延伸更自然", FaultSource: "outpaint"},
	}}
	svc.SetQualityChecker(checker)

	outcomes, err := svc.AdaptToPlatform(context.Background(), "s", []string{"src"}, []string{"extreme.banner.1920x320"}, false, nil, "theme")
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
	if got := atomic.LoadInt32(&checker.calls); got != 1 {
		t.Errorf("expected 1 review, got %d", got)
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
