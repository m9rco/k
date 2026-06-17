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
