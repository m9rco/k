package generation

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeClock drives the limiter deterministically: sleep advances virtual time
// instead of blocking, so tests assert pacing without wall-clock flakiness.
type fakeClock struct {
	t time.Time
}

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.t = c.t.Add(d)
	return nil
}

func TestRateLimiterNilIsNoOp(t *testing.T) {
	var l *rateLimiter
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("nil limiter Wait should be a no-op, got %v", err)
	}
	if l := newRateLimiter(0, 3); l != nil {
		t.Fatalf("perSecond<=0 should disable the limiter (nil), got %v", l)
	}
}

func TestRateLimiterBurstThenPaced(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	l := newRateLimiter(3, 3) // 3/s, burst 3
	l.now = clk.now
	l.sleep = clk.sleep

	start := clk.t
	// First 3 calls consume the initial burst with no virtual time elapsed.
	for i := 0; i < 3; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("burst call %d: %v", i, err)
		}
	}
	if clk.t != start {
		t.Fatalf("burst should not advance the clock; advanced by %v", clk.t.Sub(start))
	}

	// 4th call must wait for a token to refill: at 3/s that's ~1/3s.
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("4th call: %v", err)
	}
	waited := clk.t.Sub(start)
	if waited < 300*time.Millisecond || waited > 400*time.Millisecond {
		t.Fatalf("4th call should wait ~1/3s, waited %v", waited)
	}
}

func TestRateLimiterCapsAtThreePerSecond(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	l := newRateLimiter(3, 3)
	l.now = clk.now
	l.sleep = clk.sleep

	start := clk.t
	const n = 30
	for i := 0; i < n; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	// 30 calls with burst 3 means 27 paced at 3/s ≈ 9s of virtual time.
	elapsed := clk.t.Sub(start)
	rate := float64(n) / elapsed.Seconds()
	if rate > 3.5 {
		t.Fatalf("sustained rate %.2f/s exceeds the 3/s ceiling", rate)
	}
}

func TestRateLimiterWaitHonorsContext(t *testing.T) {
	l := newRateLimiter(3, 1)
	// Exhaust the single burst token.
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("first call: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the next Wait must return promptly
	err := l.Wait(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
