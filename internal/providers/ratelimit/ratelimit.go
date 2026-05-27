// Package ratelimit wraps golang.org/x/time/rate to provide a combined
// RPM (requests per minute) + TPM (tokens per minute) limiter. Providers
// typically need both: hitting RPM throttles you whether or not you're
// near TPM, and vice versa.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/time/rate"
)

// Limiter is the combined RPM + TPM gate. Call Wait before every provider
// request; pass an estimated token count so TPM is enforced as well.
//
// The token estimate doesn't need to be exact — the limiter is a "best
// effort" smoothing layer. Errors from actual provider rate limiting still
// need to be handled by the retry layer.
type Limiter struct {
	rpm   *rate.Limiter
	tpm   *rate.Limiter
	rpmN  int
	tpmN  int
}

// New constructs a Limiter with the given requests-per-minute and
// tokens-per-minute budgets. Zero on either axis disables that limiter
// (so callers can set just one).
func New(rpm, tpm int) *Limiter {
	l := &Limiter{rpmN: rpm, tpmN: tpm}
	if rpm > 0 {
		// 1 request per (60/rpm) seconds, burst = rpm.
		l.rpm = rate.NewLimiter(rate.Limit(float64(rpm)/60.0), rpm)
	}
	if tpm > 0 {
		// 1 token per (60/tpm) seconds, burst = tpm (full minute's worth
		// up-front so a single chunky request doesn't immediately starve).
		l.tpm = rate.NewLimiter(rate.Limit(float64(tpm)/60.0), tpm)
	}
	return l
}

// Wait blocks until the RPM bucket has a slot and the TPM bucket has at
// least estTokens worth of capacity. Returns ctx.Err() if cancelled.
func (l *Limiter) Wait(ctx context.Context, estTokens int) error {
	if l.rpm != nil {
		if err := l.rpm.Wait(ctx); err != nil {
			return fmt.Errorf("rpm wait: %w", err)
		}
	}
	if l.tpm != nil && estTokens > 0 {
		// rate.WaitN errors if n > burst; clamp to burst.
		burst := l.tpm.Burst()
		if estTokens > burst {
			estTokens = burst
		}
		if err := l.tpm.WaitN(ctx, estTokens); err != nil {
			return fmt.Errorf("tpm wait: %w", err)
		}
	}
	return nil
}

// AvailableRequests returns the number of slots currently available in the
// RPM bucket (estimation; the bucket changes over time).
func (l *Limiter) AvailableRequests() int {
	if l.rpm == nil {
		return 0
	}
	return int(l.rpm.TokensAt(time.Now()))
}

// Limits returns the configured (RPM, TPM) pair.
func (l *Limiter) Limits() (int, int) { return l.rpmN, l.tpmN }
