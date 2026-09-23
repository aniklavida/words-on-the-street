package fetch

import (
	"context"
	"sync"
	"time"
)

// RateLimiter enforces a minimum interval between calls to Wait. It is the
// mechanism that keeps the default twitter request rate conservative; the
// interval comes from configuration and can be shortened by raising the limit.
//
// now and sleep are fields rather than direct calls to time.Now and time.Sleep
// so a test can exercise the schedule deterministically.
type RateLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
}

// NewRateLimiter returns a limiter that allows perMinute calls per minute. A
// non-positive value yields a limiter that never waits.
func NewRateLimiter(perMinute int) *RateLimiter {
	var interval time.Duration
	if perMinute > 0 {
		interval = time.Minute / time.Duration(perMinute)
	}
	return &RateLimiter{
		interval: interval,
		now:      time.Now,
		sleep:    sleepContext,
	}
}

// Wait blocks until the minimum interval since the previous call has elapsed,
// or until the context is cancelled. The first call never blocks.
func (r *RateLimiter) Wait(ctx context.Context) error {
	if r == nil || r.interval <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	if !r.last.IsZero() {
		if wait := r.interval - now.Sub(r.last); wait > 0 {
			if err := r.sleep(ctx, wait); err != nil {
				return err
			}
			now = r.now()
		}
	}
	r.last = now
	return nil
}

// Interval returns the minimum spacing the limiter enforces.
func (r *RateLimiter) Interval() time.Duration {
	if r == nil {
		return 0
	}
	return r.interval
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Credential-bearing sources are rate limited per source. Only twitter
// registers a limiter today; the map keeps the interval per source so a later
// session source does not share twitter's budget.
var (
	credentialLimitersMu sync.Mutex
	credentialLimiters   = map[string]*RateLimiter{}
)

// credentialRateLimiter returns (creating on first use) the limiter for a
// source whose resolved request carries a credential. The interval is read
// from the configured twitter limit.
func credentialRateLimiter(source string) *RateLimiter {
	credentialLimitersMu.Lock()
	defer credentialLimitersMu.Unlock()
	if limiter, ok := credentialLimiters[source]; ok {
		return limiter
	}
	limiter := NewRateLimiter(TwitterRateLimitPerMinute())
	credentialLimiters[source] = limiter
	return limiter
}

// resetCredentialRateLimiters clears the per-source limiters so a test can
// observe limiter creation from a known state. Production code never calls it.
func resetCredentialRateLimiters() {
	credentialLimitersMu.Lock()
	defer credentialLimitersMu.Unlock()
	credentialLimiters = map[string]*RateLimiter{}
}
