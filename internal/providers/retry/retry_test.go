package retry_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/repowise-dev/repowise-go/internal/providers"
	"github.com/repowise-dev/repowise-go/internal/providers/retry"
)

func TestDo_RetriesOnTransientAndEventuallySucceeds(t *testing.T) {
	calls := 0
	err := retry.Do(context.Background(), retry.Policy{
		InitialInterval: 5 * time.Millisecond,
		MaxInterval:     20 * time.Millisecond,
		MaxElapsedTime:  500 * time.Millisecond,
		Multiplier:      1.5,
	}, func() error {
		calls++
		if calls < 3 {
			return fmt.Errorf("blip: %w", providers.ErrTransient)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestDo_DoesNotRetryNonTransient(t *testing.T) {
	calls := 0
	err := retry.Do(context.Background(), retry.Policy{
		InitialInterval: 5 * time.Millisecond,
		MaxInterval:     20 * time.Millisecond,
		MaxElapsedTime:  500 * time.Millisecond,
	}, func() error {
		calls++
		return providers.ErrUnauthenticated
	})
	if err == nil {
		t.Errorf("expected error")
	}
	if !errors.Is(err, providers.ErrUnauthenticated) {
		t.Errorf("err = %v, want ErrUnauthenticated", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry)", calls)
	}
}

func TestDo_GivesUpAfterMaxElapsed(t *testing.T) {
	calls := 0
	start := time.Now()
	err := retry.Do(context.Background(), retry.Policy{
		InitialInterval: 5 * time.Millisecond,
		MaxInterval:     10 * time.Millisecond,
		MaxElapsedTime:  50 * time.Millisecond,
		Multiplier:      1.2,
	}, func() error {
		calls++
		return fmt.Errorf("nope: %w", providers.ErrTransient)
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Errorf("expected error after exhausted backoff")
	}
	if calls < 2 {
		t.Errorf("calls = %d, want at least 2 retries", calls)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Do ran far too long: %v", elapsed)
	}
}

func TestDo_RespectsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()

	calls := 0
	err := retry.Do(ctx, retry.Policy{
		InitialInterval: 5 * time.Millisecond,
		MaxInterval:     50 * time.Millisecond,
		MaxElapsedTime:  5 * time.Second,
	}, func() error {
		calls++
		return fmt.Errorf("retry: %w", providers.ErrTransient)
	})
	if err == nil {
		t.Errorf("expected cancellation error")
	}
}
