package generation

import (
	"context"
	"sync"
	"time"
)

// rateLimiter is a minimal token-bucket throttle for provider submissions. It
// paces concurrent generation calls so a batch — e.g. adapting one source to 16
// platform sizes, which spawns 16 goroutines that would otherwise hit the image
// provider in the same instant — is spread out instead of arriving as a burst.
//
// We roll our own rather than pull in golang.org/x/time/rate so the build stays
// dependency-light and offline-friendly. Semantics match a standard token
// bucket: tokens refill at `rate` per second up to `burst`, each Wait consumes
// one, and Wait blocks (honoring ctx) until a token is available.
type rateLimiter struct {
	mu     sync.Mutex
	rate   float64 // tokens granted per second
	burst  float64 // max tokens that can accumulate (caps idle credit)
	tokens float64
	last   time.Time
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
}

// newRateLimiter builds a limiter granting perSecond tokens per second with the
// given burst. burst < 1 is clamped to 1. perSecond <= 0 returns nil, which Wait
// treats as "no throttling" — so callers can disable the limit by config.
func newRateLimiter(perSecond float64, burst int) *rateLimiter {
	if perSecond <= 0 {
		return nil
	}
	if burst < 1 {
		burst = 1
	}
	return &rateLimiter{
		rate:   perSecond,
		burst:  float64(burst),
		tokens: float64(burst), // start full: an isolated call never waits
		now:    time.Now,
		sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		},
	}
}

// Wait blocks until a token is available or ctx is cancelled. A nil limiter is a
// no-op (returns nil immediately), so an unconfigured limiter disables throttling.
func (l *rateLimiter) Wait(ctx context.Context) error {
	if l == nil {
		return nil
	}
	for {
		l.mu.Lock()
		now := l.now()
		if l.last.IsZero() {
			l.last = now
		}
		// Refill proportional to elapsed time, capped at burst.
		if elapsed := now.Sub(l.last); elapsed > 0 {
			l.tokens += elapsed.Seconds() * l.rate
			if l.tokens > l.burst {
				l.tokens = l.burst
			}
			l.last = now
		}
		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}
		// Not enough credit yet: sleep until the next token would accrue.
		deficit := 1 - l.tokens
		wait := time.Duration(deficit / l.rate * float64(time.Second))
		l.mu.Unlock()
		if wait <= 0 {
			wait = time.Millisecond
		}
		if err := l.sleep(ctx, wait); err != nil {
			return err
		}
	}
}
