package middleware_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/repowise-dev/repowise-go/internal/persistence/coststore"
	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/providers"
	"github.com/repowise-dev/repowise-go/internal/providers/middleware"
	"github.com/repowise-dev/repowise-go/internal/providers/ratelimit"
	"github.com/repowise-dev/repowise-go/internal/providers/retry"
)

// stubProvider is a controllable Provider for testing middleware. Each
// Generate call advances calls counter; if responses is non-empty it
// pops the next planned outcome.
type stubProvider struct {
	name      string
	model     string
	calls     atomic.Int32
	responses []stubResponse
}

type stubResponse struct {
	resp providers.Response
	err  error
}

func (p *stubProvider) Name() string     { return p.name }
func (p *stubProvider) Models() []string { return []string{p.model} }

func (p *stubProvider) Generate(ctx context.Context, req providers.Request) (providers.Response, error) {
	idx := int(p.calls.Add(1) - 1)
	if idx < len(p.responses) {
		r := p.responses[idx]
		return r.resp, r.err
	}
	return providers.Response{
		Model: p.model,
		Content: "ok",
		Usage:   providers.Usage{InputTokens: 100, OutputTokens: 20},
	}, nil
}

func (p *stubProvider) GenerateStream(ctx context.Context, req providers.Request) (<-chan providers.StreamEvent, error) {
	return nil, errors.New("stream not implemented")
}

func setupCostStore(t *testing.T) (*coststore.Store, string) {
	t.Helper()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{
		URL: "sqlite:" + filepath.Join(t.TempDir(), "wiki.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	r, _ := repos.New(conn).EnsureByLocalPath(ctx, t.TempDir(), "mw")
	return coststore.New(conn), r.ID
}

func TestWrap_PassesThroughOnSuccess(t *testing.T) {
	stub := &stubProvider{name: "anthropic", model: "claude-sonnet-4-6"}
	wrapped := middleware.Wrap(stub, middleware.Options{})
	resp, err := wrapped.Generate(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q", resp.Content)
	}
	if stub.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", stub.calls.Load())
	}
}

func TestWrap_RetriesOnTransientError(t *testing.T) {
	stub := &stubProvider{
		name: "anthropic", model: "claude-sonnet-4-6",
		responses: []stubResponse{
			{err: fmt.Errorf("flaky network: %w", providers.ErrTransient)},
			{err: fmt.Errorf("still flaky: %w", providers.ErrTransient)},
			{resp: providers.Response{Model: "claude-sonnet-4-6", Content: "finally", Usage: providers.Usage{InputTokens: 5, OutputTokens: 2}}},
		},
	}
	wrapped := middleware.Wrap(stub, middleware.Options{
		RetryPolicy: retry.Policy{
			InitialInterval: 1 * time.Millisecond,
			MaxInterval:     5 * time.Millisecond,
			MaxElapsedTime:  500 * time.Millisecond,
			Multiplier:      1.5,
			RandomFactor:    0.1,
		},
	})
	resp, err := wrapped.Generate(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Content != "finally" {
		t.Errorf("Content = %q", resp.Content)
	}
	if stub.calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", stub.calls.Load())
	}
}

func TestWrap_NoRetryOnPermanentError(t *testing.T) {
	stub := &stubProvider{
		name: "anthropic", model: "claude-sonnet-4-6",
		responses: []stubResponse{
			{err: fmt.Errorf("nope: %w", providers.ErrUnauthenticated)},
		},
	}
	wrapped := middleware.Wrap(stub, middleware.Options{
		RetryPolicy: retry.Policy{
			InitialInterval: 1 * time.Millisecond,
			MaxElapsedTime:  500 * time.Millisecond,
		},
	})
	_, err := wrapped.Generate(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, providers.ErrUnauthenticated) {
		t.Errorf("expected ErrUnauthenticated, got %v", err)
	}
	if stub.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (no retry on permanent)", stub.calls.Load())
	}
}

func TestWrap_PersistsCostOnSuccess(t *testing.T) {
	store, repoID := setupCostStore(t)
	stub := &stubProvider{
		name: "anthropic", model: "claude-sonnet-4-6",
		responses: []stubResponse{
			{resp: providers.Response{
				Model: "claude-sonnet-4-6",
				Content: "ok",
				Usage:   providers.Usage{InputTokens: 1_000_000, OutputTokens: 200_000, CachedTokens: 500_000},
			}},
		},
	}
	wrapped := middleware.Wrap(stub, middleware.Options{
		RepositoryID: repoID,
		CostStore:    store,
	})
	_, err := wrapped.Generate(context.Background(), providers.Request{
		Operation: "file_overview",
		FilePath:  "x.go",
		Messages:  []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	count, err := store.Count(context.Background(), repoID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("cost rows = %d, want 1", count)
	}
	total, err := store.TotalUSD(context.Background(), repoID)
	if err != nil {
		t.Fatal(err)
	}
	// 1M input @ $3 + 200K output @ $15 + 500K cached @ $0.3
	//   = $3 + $3 + $0.15 = $6.15
	want := 6.15
	if total < want-0.001 || total > want+0.001 {
		t.Errorf("total = %v, want ≈ %v", total, want)
	}
}

func TestWrap_NoCostPersistedOnFailure(t *testing.T) {
	store, repoID := setupCostStore(t)
	stub := &stubProvider{
		name: "anthropic", model: "claude-sonnet-4-6",
		responses: []stubResponse{
			{err: fmt.Errorf("nope: %w", providers.ErrUnauthenticated)},
		},
	}
	wrapped := middleware.Wrap(stub, middleware.Options{
		RepositoryID: repoID,
		CostStore:    store,
		RetryPolicy: retry.Policy{
			InitialInterval: 1 * time.Millisecond,
			MaxElapsedTime:  50 * time.Millisecond,
		},
	})
	_, err := wrapped.Generate(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	count, _ := store.Count(context.Background(), repoID)
	if count != 0 {
		t.Errorf("cost rows = %d, want 0 (failed call must not persist)", count)
	}
}

func TestWrap_RatelimitBlocks(t *testing.T) {
	stub := &stubProvider{name: "anthropic", model: "claude-sonnet-4-6"}
	// 60 RPM = 1 request/sec, burst 60 — first request goes through
	// immediately, second has to wait one second. Use a 100ms deadline
	// to assert blocking actually happens.
	limiter := ratelimit.New(60, 0)
	wrapped := middleware.Wrap(stub, middleware.Options{Limiter: limiter})

	// Drain the burst.
	for i := 0; i < 60; i++ {
		_, _ = wrapped.Generate(context.Background(), providers.Request{
			Messages: []providers.Message{{Role: providers.RoleUser, Content: "x"}},
		})
	}
	// 61st request should block until the limiter refills.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := wrapped.Generate(ctx, providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Error("expected context-deadline error after burst exhausted")
	}
}

func TestWrap_PassthroughNameAndModels(t *testing.T) {
	stub := &stubProvider{name: "openai", model: "gpt-4o"}
	wrapped := middleware.Wrap(stub, middleware.Options{})
	if wrapped.Name() != "openai" {
		t.Errorf("Name() = %q", wrapped.Name())
	}
	if got := wrapped.Models(); len(got) != 1 || got[0] != "gpt-4o" {
		t.Errorf("Models() = %v", got)
	}
}

func TestWrap_CostStoreFailureDoesNotFailRequest(t *testing.T) {
	// CostStore=nil should leave the request unaffected.
	stub := &stubProvider{name: "anthropic", model: "claude-sonnet-4-6"}
	wrapped := middleware.Wrap(stub, middleware.Options{
		RepositoryID: "ghost-repo",
		// CostStore intentionally nil.
	})
	_, err := wrapped.Generate(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}
