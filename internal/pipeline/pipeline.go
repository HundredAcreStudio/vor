// Package pipeline orchestrates the full ingest flow as a sequence of
// named phases, recording each one in pipeline_jobs. Mirrors the Python
// `core.pipeline.orchestrator` shape: each phase is a function that
// receives the current state, mutates it, and reports completion.
//
// Pass A delivers phase tracking + a clean Run() entrypoint. Resume on
// failure (replay completed phases from the previous run) lands in
// Pass B when the cursor column gains real use.
package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/repowise-dev/repowise-go/internal/analysis/deadcode"
	"github.com/repowise-dev/repowise-go/internal/analysis/health"
	"github.com/repowise-dev/repowise-go/internal/ingestion/external"
	"github.com/repowise-dev/repowise-go/internal/ingestion/git"
	"github.com/repowise-dev/repowise-go/internal/ingestion/graph"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser"
	"github.com/repowise-dev/repowise-go/internal/ingestion/traverser"
	"github.com/repowise-dev/repowise-go/internal/persistence/deadstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/externalstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/gitstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/graphstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/healthstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/pipelinestore"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

// Mode enumerates the pipeline execution modes. Mirrors the Python
// PipelineMode enum.
type Mode string

const (
	// ModeInit is the first-time index of a repository.
	ModeInit Mode = "init"
	// ModeUpdate is an incremental re-index (no-op vs init for now;
	// distinguishes user intent in pipeline_jobs.metadata).
	ModeUpdate Mode = "update"
)

// Phase names. Stored as pipeline_jobs.phase. Order is significant —
// Run executes them top-to-bottom.
const (
	PhaseTraverse  = "traverse"
	PhaseParse     = "parse"
	PhaseGit       = "git"
	PhaseGraph     = "graph"
	PhaseDeadCode  = "deadcode"
	PhaseHealth    = "health"
	PhaseExternals = "externals"
	PhasePersist   = "persist"
)

// Options configures a pipeline run.
type Options struct {
	// RepoPath is the absolute path to the repository to index. Required.
	RepoPath string

	// Mode signals INIT vs UPDATE. Recorded in pipeline_jobs.metadata_json
	// for observability; both modes currently run the same phase sequence.
	Mode Mode

	// DB is the open *sql.DB. Required.
	DB *sql.DB

	// RepositoryID is the row this run attributes pipeline_jobs to.
	// Required.
	RepositoryID string

	// Logger receives one structured event per phase start / finish.
	// Defaults to slog.Default() if nil.
	Logger *slog.Logger

	// GitMaxCommits caps the git intelligence walk (0 = default 10000).
	GitMaxCommits int
}

// Result is the bundle the caller can persist or inspect after Run.
type Result struct {
	Files        []models.FileInfo
	Parsed       []models.ParsedFile
	Graph        *graph.Graph
	GitRecords   []git.PerFile
	DeadCode     []deadcode.Finding
	HealthResult health.Result
	Externals    []external.Record
	TraversalStats models.TraversalStats

	// Phases records the IDs of pipeline_jobs rows written this run, in
	// execution order. Useful for tests + the "show pipeline history"
	// CLI surface.
	Phases []PhaseRecord
}

// PhaseRecord is one line in the run log.
type PhaseRecord struct {
	Phase    string
	JobID    string
	Duration time.Duration
	State    string
	Error    string
}

// Run executes the full pipeline. Each phase is wrapped in
// pipelinestore Start/Complete/Fail. A failure halts the run and returns
// the partial Result + error so the caller can decide what to persist.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.RepoPath == "" {
		return nil, errors.New("opts.RepoPath is required")
	}
	if opts.DB == nil {
		return nil, errors.New("opts.DB is required")
	}
	if opts.RepositoryID == "" {
		return nil, errors.New("opts.RepositoryID is required")
	}
	if opts.Mode == "" {
		opts.Mode = ModeInit
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	store := pipelinestore.New(opts.DB)
	res := &Result{}

	// Phase: traverse.
	if err := runPhase(ctx, opts, store, PhaseTraverse, res, func() error {
		tr, err := traverser.New(traverser.Options{RepoRoot: opts.RepoPath})
		if err != nil {
			return err
		}
		files, stats, err := tr.Collect(ctx)
		if err != nil {
			return err
		}
		res.Files = files
		res.TraversalStats = stats
		return nil
	}); err != nil {
		return res, err
	}

	// Phase: parse.
	if err := runPhase(ctx, opts, store, PhaseParse, res, func() error {
		ap := parser.New()
		for _, fi := range res.Files {
			if parser.LookupParser(fi.Language) == nil {
				continue
			}
			data, err := readFile(fi.AbsPath)
			if err != nil {
				continue
			}
			pf, err := ap.Parse(ctx, fi, data)
			if err != nil {
				continue // tolerate per-file parse errors
			}
			res.Parsed = append(res.Parsed, pf)
		}
		return nil
	}); err != nil {
		return res, err
	}

	// Phase: git (best-effort — not all dirs are git repos).
	_ = runPhase(ctx, opts, store, PhaseGit, res, func() error {
		ix := &git.Indexer{MaxCommits: opts.GitMaxCommits}
		recs, err := ix.Index(ctx, opts.RepoPath)
		if err != nil {
			// Non-fatal; degrade gracefully.
			opts.Logger.Warn("git intelligence skipped", "err", err)
			return nil
		}
		res.GitRecords = recs
		return nil
	})

	// Phase: graph.
	if err := runPhase(ctx, opts, store, PhaseGraph, res, func() error {
		b := graph.NewBuilder(nil, graph.Options{})
		for _, p := range res.Parsed {
			b.AddFile(p)
		}
		g := b.Build()
		g.ComputeMetrics()
		res.Graph = g
		return nil
	}); err != nil {
		return res, err
	}

	// Phase: deadcode.
	if err := runPhase(ctx, opts, store, PhaseDeadCode, res, func() error {
		res.DeadCode = (&deadcode.Analyzer{MinConfidence: 0.5}).Analyze(res.Graph)
		return nil
	}); err != nil {
		return res, err
	}

	// Phase: health.
	if err := runPhase(ctx, opts, store, PhaseHealth, res, func() error {
		analyzer := &health.Analyzer{}
		if len(res.GitRecords) > 0 {
			hot := make(map[string]struct{}, len(res.GitRecords))
			for _, gr := range res.GitRecords {
				if gr.IsHotspot {
					hot[gr.Path] = struct{}{}
				}
			}
			analyzer.HotspotPaths = hot
		}
		res.HealthResult = analyzer.Analyze(res.Parsed)
		return nil
	}); err != nil {
		return res, err
	}

	// Phase: externals.
	if err := runPhase(ctx, opts, store, PhaseExternals, res, func() error {
		recs, err := external.ScanRoot(ctx, opts.RepoPath)
		if err != nil {
			return err
		}
		res.Externals = recs
		return nil
	}); err != nil {
		return res, err
	}

	// Phase: persist (writes all stores in one tx-per-table). Reuses the
	// existing per-store ReplaceAll APIs.
	if err := runPhase(ctx, opts, store, PhasePersist, res, func() error {
		repoStore := repos.New(opts.DB)
		if head, err := git.ResolveHeadCommit(opts.RepoPath); err == nil {
			_ = repoStore.UpdateHeadCommit(ctx, opts.RepositoryID, head.String())
		}
		if err := graphstore.New(opts.DB).ReplaceGraph(ctx, opts.RepositoryID, res.Graph); err != nil {
			return fmt.Errorf("graph: %w", err)
		}
		if err := gitstore.New(opts.DB).ReplaceAll(ctx, opts.RepositoryID, res.GitRecords); err != nil {
			return fmt.Errorf("git_metadata: %w", err)
		}
		if err := deadstore.New(opts.DB).ReplaceAll(ctx, opts.RepositoryID, res.DeadCode); err != nil {
			return fmt.Errorf("dead_code: %w", err)
		}
		if err := healthstore.New(opts.DB).ReplaceAll(ctx, opts.RepositoryID, res.HealthResult); err != nil {
			return fmt.Errorf("health: %w", err)
		}
		if err := externalstore.New(opts.DB).ReplaceAll(ctx, opts.RepositoryID, res.Externals); err != nil {
			return fmt.Errorf("externals: %w", err)
		}
		return nil
	}); err != nil {
		return res, err
	}

	return res, nil
}

// runPhase is the small helper that handles the start/complete/fail
// bookkeeping. The caller supplies a closure with the actual work.
func runPhase(ctx context.Context, opts Options, store *pipelinestore.Store, phase string, res *Result, do func() error) error {
	start := time.Now()
	job, err := store.Begin(ctx, opts.RepositoryID, phase)
	if err != nil {
		return fmt.Errorf("begin %s: %w", phase, err)
	}
	if err := store.Start(ctx, job.ID); err != nil {
		return fmt.Errorf("start %s: %w", phase, err)
	}
	opts.Logger.Info("pipeline phase start", "phase", phase, "job", job.ID, "mode", opts.Mode)

	rec := PhaseRecord{Phase: phase, JobID: job.ID}
	phaseErr := do()
	rec.Duration = time.Since(start)

	if phaseErr != nil {
		rec.State = pipelinestore.StateFailed
		rec.Error = phaseErr.Error()
		_ = store.Fail(ctx, job.ID, phaseErr.Error())
		opts.Logger.Error("pipeline phase failed",
			"phase", phase, "job", job.ID, "duration_ms", rec.Duration.Milliseconds(), "err", phaseErr)
	} else {
		rec.State = pipelinestore.StateCompleted
		_ = store.Complete(ctx, job.ID)
		opts.Logger.Info("pipeline phase complete",
			"phase", phase, "job", job.ID, "duration_ms", rec.Duration.Milliseconds())
	}
	res.Phases = append(res.Phases, rec)
	return phaseErr
}

// readFile is a tiny indirection so tests can intercept I/O. Plain
// os.ReadFile for now; pluggable later if the pipeline grows a virtual
// filesystem abstraction.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// AbsRepoPath resolves a possibly-relative path to absolute. Surface so
// callers don't need to import path/filepath just for this.
func AbsRepoPath(p string) (string, error) {
	return filepath.Abs(p)
}
