package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/HundredAcreStudio/vor/internal/providers/ratelimit"
)

func TestLimiter_RpmGate(t *testing.T) {
	// 60 RPM = 1 request per second, burst 60. After the burst is drained,
	// the next request should block for ~1s.
	l := ratelimit.New(60, 0)
	ctx := context.Background()

	// Drain the burst.
	for i := 0; i < 60; i++ {
		if err := l.Wait(ctx, 0); err != nil {
			t.Fatalf("Wait #%d: %v", i, err)
		}
	}
	start := time.Now()
	if err := l.Wait(ctx, 0); err != nil {
		t.Fatalf("Wait (post-burst): %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 500*time.Millisecond {
		t.Errorf("expected ~1s wait after burst drain, got %v", elapsed)
	}
}

func TestLimiter_TpmGate(t *testing.T) {
	// 100k TPM ≈ 1667 tokens/sec. A 50k-token request should drain ~30s of
	// budget. Asking for the same 50k twice in a row should block on the
	// second.
	l := ratelimit.New(0, 100_000)
	ctx := context.Background()

	if err := l.Wait(ctx, 50_000); err != nil {
		t.Fatalf("Wait #1: %v", err)
	}
	if err := l.Wait(ctx, 50_000); err != nil {
		t.Fatalf("Wait #2: %v", err)
	}
	// Third 50k request can't fit (we've now used 100k of the 100k burst).
	// Capping ctx so we don't wait forever.
	ctxTO, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctxTO, 50_000); err == nil {
		t.Errorf("expected timeout when TPM bucket is drained")
	}
}

func TestLimiter_ZeroDisables(t *testing.T) {
	// New(0, 0) should never block on Wait regardless of estTokens.
	l := ratelimit.New(0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	for i := 0; i < 100; i++ {
		if err := l.Wait(ctx, 1_000_000); err != nil {
			t.Fatalf("Wait #%d: %v", i, err)
		}
	}
}

func TestLimiter_LimitsExposed(t *testing.T) {
	l := ratelimit.New(123, 456)
	rpm, tpm := l.Limits()
	if rpm != 123 || tpm != 456 {
		t.Errorf("Limits() = (%d, %d), want (123, 456)", rpm, tpm)
	}
}

func TestLimiter_ContextCancelStopsWait(t *testing.T) {
	l := ratelimit.New(1, 0) // 1 RPM
	ctx, cancel := context.WithCancel(context.Background())

	// First call consumes the burst — succeeds instantly.
	if err := l.Wait(ctx, 0); err != nil {
		t.Fatalf("first wait: %v", err)
	}
	// Second call would block for ~60s. Cancel after 50ms.
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	err := l.Wait(ctx, 0)
	elapsed := time.Since(start)
	if err == nil {
		t.Errorf("expected ctx-cancelled error")
	}
	if elapsed > 5*time.Second {
		t.Errorf("Wait did not respect ctx cancel: blocked %v", elapsed)
	}
}
