// Package middleware composes the cost / ratelimit / retry concerns
// around a raw providers.Provider. The chain — outermost to innermost —
// is:
//
//	caller → ratelimit.Wait → retry.Do(raw provider) → cost recording
//
// Order matters:
//   - ratelimit is outermost so over-budget callers block before we
//     spend a network slot.
//   - retry wraps the provider call so transient 5xx/429 retries
//     happen inside the rate-limit window (the limiter already paid
//     for this request).
//   - cost recording runs after a successful response, with the live
//     token counts.
//
// Streaming is not wrapped — generation streams are interactive UX
// territory and don't fit a "retry the whole stream" semantic. Callers
// that need streaming should hold the raw provider reference.
package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/repowise-dev/repowise-go/internal/persistence/coststore"
	"github.com/repowise-dev/repowise-go/internal/providers"
	"github.com/repowise-dev/repowise-go/internal/providers/cost"
	"github.com/repowise-dev/repowise-go/internal/providers/ratelimit"
	"github.com/repowise-dev/repowise-go/internal/providers/retry"
)

// Options configures the wrapper.
type Options struct {
	// RepositoryID is stamped on every cost row. Empty disables cost
	// persistence (still computes cost; just doesn't write it).
	RepositoryID string

	// CostStore writes llm_costs rows. Nil disables persistence (the
	// CostUSD field still appears in any returned metadata).
	CostStore *coststore.Store

	// Limiter throttles request rate. Nil disables limiting.
	Limiter *ratelimit.Limiter

	// RetryPolicy configures backoff. Zero value → retry.Default().
	RetryPolicy retry.Policy

	// Logger receives one event per request (success or terminal
	// failure). Nil → slog.Default().
	Logger *slog.Logger
}

// Wrap returns a Provider that applies the middleware chain around inner.
// inner's Name() / Models() / GenerateStream() are passed through
// unchanged — only Generate is wrapped.
func Wrap(inner providers.Provider, opts Options) providers.Provider {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &wrapped{inner: inner, opts: opts}
}

type wrapped struct {
	inner providers.Provider
	opts  Options
}

func (w *wrapped) Name() string     { return w.inner.Name() }
func (w *wrapped) Models() []string { return w.inner.Models() }

// Generate runs the request through ratelimit → retry → inner, then
// records cost on success.
func (w *wrapped) Generate(ctx context.Context, req providers.Request) (providers.Response, error) {
	if w.opts.Limiter != nil {
		// Estimate token usage for TPM accounting. Input + a small buffer
		// for the output we don't yet know about. Refined post-call could
		// over-spend the bucket but that's acceptable smoothing.
		est := estimateTokens(req)
		if err := w.opts.Limiter.Wait(ctx, est); err != nil {
			return providers.Response{}, err
		}
	}

	var resp providers.Response
	start := time.Now()
	err := retry.Do(ctx, w.opts.RetryPolicy, func() error {
		r, callErr := w.inner.Generate(ctx, req)
		if callErr != nil {
			return callErr
		}
		resp = r
		return nil
	})
	if err != nil {
		w.opts.Logger.Error("provider call failed",
			"provider", w.inner.Name(),
			"operation", req.Operation,
			"duration_ms", time.Since(start).Milliseconds(),
			"err", err,
		)
		return providers.Response{}, err
	}

	costUSD := cost.EstimateFor(w.inner.Name(), resp.Model, resp.Usage)
	w.opts.Logger.Info("provider call ok",
		"provider", w.inner.Name(),
		"model", resp.Model,
		"operation", req.Operation,
		"input_tokens", resp.Usage.InputTokens,
		"output_tokens", resp.Usage.OutputTokens,
		"cached_tokens", resp.Usage.CachedTokens,
		"cost_usd", costUSD,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	if w.opts.CostStore != nil && w.opts.RepositoryID != "" {
		err := w.opts.CostStore.Insert(ctx, coststore.Entry{
			RepositoryID: w.opts.RepositoryID,
			Model:        resp.Model,
			Operation:    req.Operation,
			Usage:        resp.Usage,
			CostUSD:      costUSD,
			FilePath:     req.FilePath,
		})
		if err != nil {
			// Persistence failure shouldn't fail the request — the
			// generation succeeded; the row is for accounting.
			w.opts.Logger.Warn("cost persist failed", "err", err)
		}
	}
	return resp, nil
}

// GenerateStream passes through unchanged. See package doc.
func (w *wrapped) GenerateStream(ctx context.Context, req providers.Request) (<-chan providers.StreamEvent, error) {
	return w.inner.GenerateStream(ctx, req)
}

// estimateTokens is the rough TPM-budget estimate: 4 chars ≈ 1 token,
// plus a fixed overhead for the system prompt + role markers. Tighter
// estimates aren't worth the complexity — the limiter is smoothing,
// not enforcement.
func estimateTokens(req providers.Request) int {
	total := len(req.System) / 4
	for _, m := range req.Messages {
		total += len(m.Content) / 4
	}
	total += req.MaxTokens
	if total < 1 {
		total = 1
	}
	return total
}
