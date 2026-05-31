// Package tasks is a registry of optional post-pipeline tasks. A task is a
// unit of enrichment that runs after the analysis pipeline has indexed a repo
// — wiki page generation is the first, with embeddings, summaries, etc. able
// to follow. Tasks self-register (like parsers and providers) and are toggled
// per-repo via DB-backed settings surfaced in the dashboard, so a repo that
// doesn't want wiki generation simply disables that task.
//
// The pipeline itself stays deterministic and offline-capable; tasks are the
// place where expensive, networked, or LLM-driven work lives.
package tasks

import (
	"context"
	"database/sql"
	"log/slog"
	"sort"
	"sync"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/providerfactory"
	"github.com/HundredAcreStudio/vor/internal/providers"
)

// Task is one registerable post-pipeline unit of work.
type Task interface {
	// ID is the stable identifier persisted in settings (e.g.
	// "wiki_generation"). Keep it stable — it keys per-repo enablement.
	ID() string
	// Name is the human-facing label shown in the dashboard.
	Name() string
	// Description is a one-line explanation for the dashboard.
	Description() string
	// DefaultEnabled is whether the task runs for a repo that has expressed
	// no preference. A task may still self-skip at Run time (e.g. wiki
	// generation skips when no LLM provider is configured).
	DefaultEnabled() bool
	// Run executes the task for one repo. Returning an error records the
	// failure but does not abort other tasks.
	Run(ctx context.Context, tc Context) (Result, error)
}

// Requirement labels a capability a task needs to do real work. A task that
// requires one self-skips at Run time when it isn't configured; the dashboard
// uses the same labels to explain why a task won't run yet.
const (
	RequiresProvider = "provider" // an LLM provider (wiki generation, etc.)
	RequiresEmbedder = "embedder" // an embedding model (semantic embeddings)
)

// Requirer is an optional interface a Task may implement to declare what it
// needs to run. Tasks that don't implement it are assumed to require nothing.
type Requirer interface{ Requires() []string }

// RequirementsOf returns a task's declared requirements, or nil.
func RequirementsOf(t Task) []string {
	if r, ok := t.(Requirer); ok {
		return r.Requires()
	}
	return nil
}

// Ordered is an optional interface a Task may implement to control run order.
// Tasks run in ascending Order; ties (and tasks that don't implement it,
// treated as defaultOrder) break by ID. Use it when one task's output feeds
// another — e.g. embedding generation (higher Order) runs after wiki
// generation (lower Order) so there are pages to embed.
type Ordered interface{ Order() int }

// defaultOrder is the run-order assigned to tasks that don't implement Ordered.
const defaultOrder = 100

func orderOf(t Task) int {
	if o, ok := t.(Ordered); ok {
		return o.Order()
	}
	return defaultOrder
}

// Context is everything a task needs to run against one repo.
type Context struct {
	DB           *sql.DB
	RepositoryID string
	RepoRoot     string
	// Provider and Model are the resolved LLM provider for this repo, or
	// (nil, "") when none is configured. LLM-driven tasks skip when nil.
	Provider providers.Provider
	Model    string
	// Embedder is the resolved embedding model for this repo, or nil when
	// none is configured (or only the mock fallback). Embedding tasks skip
	// when nil.
	Embedder providers.Embedder
	Logger   *slog.Logger

	// Incremental is true when the index that triggered these tasks was an
	// incremental update (the daemon's file-watch path), so Changed is
	// authoritative. False for a full init/reindex — tasks should treat every
	// unit as potentially affected.
	Incremental bool
	// Changed lists the repo-relative file paths added or modified by the
	// triggering index. Only meaningful when Incremental is true; lets tasks
	// (e.g. wiki generation) work on just what changed instead of rescanning.
	Changed []string
}

// Result is a task's self-reported outcome, for logs and CLI/UI display.
type Result struct {
	// Skipped is true when the task chose not to do work (e.g. disabled at
	// the unit level, or no provider). Detail explains why.
	Skipped bool
	// Detail is a short human-readable summary ("12 pages generated, 3
	// skipped" or "no LLM provider configured").
	Detail string
}

var (
	mu       sync.RWMutex
	registry = map[string]Task{}
)

// Register adds a task to the global registry. Intended to be called from a
// task package's init(). Panics on a duplicate ID — that's a programming
// error, caught at startup.
func Register(t Task) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[t.ID()]; dup {
		panic("tasks: duplicate task ID " + t.ID())
	}
	registry[t.ID()] = t
}

// Registered returns all registered tasks in run order: ascending Order, then
// by ID for a stable tiebreak.
func Registered() []Task {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Task, 0, len(registry))
	for _, t := range registry {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		oi, oj := orderOf(out[i]), orderOf(out[j])
		if oi != oj {
			return oi < oj
		}
		return out[i].ID() < out[j].ID()
	})
	return out
}

// Get returns the registered task with the given ID, or (nil, false).
func Get(id string) (Task, bool) {
	mu.RLock()
	defer mu.RUnlock()
	t, ok := registry[id]
	return t, ok
}

// Enabled reports whether the task is enabled given a repo's resolved
// overrides map (config.Config.Tasks). An explicit entry wins; otherwise the
// task's DefaultEnabled applies.
func Enabled(t Task, overrides map[string]bool) bool {
	if v, ok := overrides[t.ID()]; ok {
		return v
	}
	return t.DefaultEnabled()
}

// Outcome pairs a task with the result (or error) of running it.
type Outcome struct {
	TaskID string
	Result Result
	Err    error
}

// RunEnabled runs every registered task that is enabled for this repo (per
// overrides) and returns one Outcome per task that ran. A task error is
// captured in its Outcome; it never aborts the others.
func RunEnabled(ctx context.Context, tc Context, overrides map[string]bool) []Outcome {
	var outcomes []Outcome
	for _, t := range Registered() {
		if !Enabled(t, overrides) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return outcomes
		}
		res, err := t.Run(ctx, tc)
		outcomes = append(outcomes, Outcome{TaskID: t.ID(), Result: res, Err: err})
	}
	return outcomes
}

// PipelineOutcome carries the just-completed index's change summary so
// post-pipeline tasks can work incrementally. The zero value means "full
// index" — Incremental false, no specific changed set — which is correct for
// init and reindex.
type PipelineOutcome struct {
	// Incremental is true for the daemon's file-watch update path.
	Incremental bool
	// Changed lists the repo-relative paths added or modified this index.
	Changed []string
}

// AfterPipeline is the single post-pipeline entry point used by every code
// path that indexes a repo (init, reindex, and the auto-reindex watcher). It
// resolves the repo's config + LLM provider, then runs the enabled tasks.
// Failures are logged, not returned, so a generation hiccup never fails an
// otherwise-successful index. idx describes what the triggering index changed,
// so incremental-aware tasks can scope their work.
func AfterPipeline(ctx context.Context, conn *sql.DB, repoID, repoRoot string, logger *slog.Logger, idx PipelineOutcome) []Outcome {
	if logger == nil {
		logger = slog.Default()
	}
	cfg, err := config.Resolve(ctx, conn, repoID, config.LoadBootstrap())
	if err != nil {
		logger.Warn("tasks: config resolve failed, skipping post-pipeline tasks", "repo", repoID, "err", err)
		return nil
	}
	provider, model := providerfactory.Optional(cfg)
	tc := Context{
		DB:           conn,
		RepositoryID: repoID,
		RepoRoot:     repoRoot,
		Provider:     provider,
		Model:        model,
		Embedder:     providerfactory.OptionalEmbedder(cfg),
		Logger:       logger,
		Incremental:  idx.Incremental,
		Changed:      idx.Changed,
	}
	outcomes := RunEnabled(ctx, tc, cfg.Tasks)
	for _, o := range outcomes {
		switch {
		case o.Err != nil:
			logger.Error("tasks: task failed", "task", o.TaskID, "err", o.Err)
		case o.Result.Skipped:
			logger.Debug("tasks: task skipped", "task", o.TaskID, "detail", o.Result.Detail)
		default:
			logger.Info("tasks: task done", "task", o.TaskID, "detail", o.Result.Detail)
		}
	}
	return outcomes
}
