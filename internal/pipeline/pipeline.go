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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/repowise-dev/repowise-go/internal/analysis/deadcode"
	"github.com/repowise-dev/repowise-go/internal/analysis/decisions"
	"github.com/repowise-dev/repowise-go/internal/analysis/health"
	"github.com/repowise-dev/repowise-go/internal/ingestion/external"
	"github.com/repowise-dev/repowise-go/internal/ingestion/external/cargo"
	"github.com/repowise-dev/repowise-go/internal/ingestion/external/gomod"
	"github.com/repowise-dev/repowise-go/internal/ingestion/git"
	"github.com/repowise-dev/repowise-go/internal/ingestion/graph"
	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser"
	"github.com/repowise-dev/repowise-go/internal/ingestion/traverser"

	// Side-effect imports — each per-language resolver registers itself
	// with the resolver registry in its init(). Add a language here as
	// soon as a resolver for it exists.
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/cpp"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/csharp"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/golang"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/java"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/python"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/rust"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/typescript"

	// Side-effect imports — each decision-source extractor registers
	// itself with the decisions registry in its init(). Add a source
	// here as soon as its package exists.
	_ "github.com/repowise-dev/repowise-go/internal/analysis/decisions/adr"
	_ "github.com/repowise-dev/repowise-go/internal/analysis/decisions/changelog"
	_ "github.com/repowise-dev/repowise-go/internal/analysis/decisions/commits"
	_ "github.com/repowise-dev/repowise-go/internal/analysis/decisions/inline"

	"github.com/repowise-dev/repowise-go/internal/persistence/coveragestore"
	"github.com/repowise-dev/repowise-go/internal/persistence/deadstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/decisionstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/externalstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/gitstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/graphstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/healthstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/parsestore"
	"github.com/repowise-dev/repowise-go/internal/persistence/pipelinestore"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

// Mode enumerates the pipeline execution modes. Mirrors the Python
// PipelineMode enum.
type Mode string

const (
	// ModeInit is the first-time index of a repository.
	ModeInit Mode = "init"
	// ModeUpdate is an incremental re-index. The parse phase reuses cached
	// parse results for files whose content is unchanged (see parserVersion
	// + parsestore), re-parsing only changed/new files and pruning deleted
	// ones. Init and update run the same phases; the cache is what makes
	// update cheap on large repos.
	ModeUpdate Mode = "update"
	// ModeResume continues the most recent unfinished run for the repo.
	// At the start of Run() we look up the latest run via
	// pipelinestore.LatestRun; if it's running or failed, we adopt its
	// run_id so the new pipeline_jobs rows link back. If the latest run
	// already succeeded, Run() returns ErrNothingToResume.
	ModeResume Mode = "resume"
)

// ErrNothingToResume is returned when Mode=ModeResume but the latest
// run for the repo has already finished successfully — there's no
// failure to continue from.
var ErrNothingToResume = errors.New("pipeline: latest run already succeeded; nothing to resume")

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
	PhaseDecisions = "decisions"
	PhasePersist   = "persist"
)

// parserVersion tags parse-cache rows. Bump it whenever a grammar upgrade
// or extraction change could alter parse output for unchanged source, so
// stale cache entries are transparently invalidated.
const parserVersion = "1"

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

	// RunID groups every phase of this run together in pipeline_jobs
	// for resume + observability. When empty, Run() generates one. When
	// Mode=ModeResume, an explicit RunID overrides the auto-detected
	// "latest unfinished run" lookup.
	RunID string
}

// Result is the bundle the caller can persist or inspect after Run.
type Result struct {
	Files          []models.FileInfo
	Parsed         []models.ParsedFile
	Graph          *graph.Graph
	GitRecords     []git.PerFile
	DeadCode       []deadcode.Finding
	HealthResult   health.Result
	Externals      []external.Record
	Decisions      []decisions.Record
	TraversalStats models.TraversalStats

	// ParseStats reports incremental-parse outcomes for the run: how many
	// files were freshly parsed vs reused from the cache, and how many
	// stale cache rows were pruned.
	ParseStats ParseStats

	// Phases records the IDs of pipeline_jobs rows written this run, in
	// execution order. Useful for tests + the "show pipeline history"
	// CLI surface.
	Phases []PhaseRecord

	// RunID is the run_id used for this invocation. Generated when
	// Options.RunID is empty; echoes the user-supplied value otherwise.
	// Useful for resume — a caller can ask "what run did I just kick
	// off?" without parsing pipeline_jobs.
	RunID string
}

// ParseStats summarises the incremental-parse outcome for a run.
type ParseStats struct {
	Total  int // files with a registered parser
	Parsed int // freshly parsed (changed/new or cache miss)
	Reused int // served from the parse cache (unchanged)
	Pruned int // stale cache rows removed (deleted files)
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

	// Resolve run_id. Mode=ModeResume looks up the latest unfinished
	// run for the repo and adopts its run_id; an explicit opts.RunID
	// overrides. Other modes generate a fresh run_id.
	runID := opts.RunID
	if runID == "" {
		switch opts.Mode {
		case ModeResume:
			latest, err := store.LatestRun(ctx, opts.RepositoryID)
			if err != nil {
				return nil, fmt.Errorf("lookup latest run: %w", err)
			}
			if latest == nil {
				return nil, errors.New("pipeline: no previous run found to resume")
			}
			if latest.Overall == pipelinestore.OutcomeSucceeded {
				return nil, ErrNothingToResume
			}
			runID = latest.RunID
		default:
			runID = newRunID()
		}
	}
	res := &Result{RunID: runID}

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

	// Phase: parse. Incremental: files whose content hash + parser version
	// match a cached entry skip the (expensive, cgo) tree-sitter parse and
	// reuse the stored result. Changed/new files are re-parsed and cached;
	// deleted files are pruned from the cache.
	if err := runPhase(ctx, opts, store, PhaseParse, res, func() error {
		ap := parser.New()
		useCache := opts.DB != nil && opts.RepositoryID != ""
		pstore := parsestore.New(opts.DB)

		var cache map[string]parsestore.Cached
		if useCache {
			if c, err := pstore.LoadAll(ctx, opts.RepositoryID, parserVersion); err == nil {
				cache = c
			}
		}

		var dirty []parsestore.Entry
		var keep []string
		var stats ParseStats
		for _, fi := range res.Files {
			if parser.LookupParser(fi.Language) == nil {
				continue
			}
			data, err := readFile(fi.AbsPath)
			if err != nil {
				continue
			}
			stats.Total++
			keep = append(keep, fi.Path)
			h := sha256Hex(data)

			if c, ok := cache[fi.Path]; ok && c.ContentHash == h {
				var pf models.ParsedFile
				if json.Unmarshal(c.ParsedJSON, &pf) == nil {
					pf.FileInfo = fi // re-attach the live FileInfo
					res.Parsed = append(res.Parsed, pf)
					stats.Reused++
					continue
				}
			}

			pf, err := ap.Parse(ctx, fi, data)
			if err != nil {
				continue // tolerate per-file parse errors
			}
			res.Parsed = append(res.Parsed, pf)
			stats.Parsed++
			// Cache without the volatile FileInfo (paths/mtimes come from the
			// live traversal on reload).
			cp := pf
			cp.FileInfo = models.FileInfo{}
			if b, mErr := json.Marshal(cp); mErr == nil {
				dirty = append(dirty, parsestore.Entry{Path: fi.Path, ContentHash: h, ParsedJSON: b})
			}
		}

		if useCache {
			if err := pstore.Upsert(ctx, opts.RepositoryID, parserVersion, dirty); err != nil {
				opts.Logger.Warn("parse cache upsert failed", "err", err)
			}
			if n, err := pstore.Prune(ctx, opts.RepositoryID, keep); err != nil {
				opts.Logger.Warn("parse cache prune failed", "err", err)
			} else {
				stats.Pruned = n
			}
		}
		res.ParseStats = stats
		opts.Logger.Info("parse phase", "parsed", stats.Parsed, "reused", stats.Reused,
			"pruned", stats.Pruned, "total", stats.Total)
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

	// Phase: graph. Builds the dependency graph using the per-language
	// resolver registry. Reads go.mod (and similar per-language config
	// files in future) so resolvers have the cross-cutting data they
	// need to translate module-path imports into file paths.
	if err := runPhase(ctx, opts, store, PhaseGraph, res, func() error {
		rctx := resolver.Context{}
		if modPath, err := gomod.ParseModulePath(opts.RepoPath); err == nil && modPath != "" {
			rctx.GoModulePath = modPath
			opts.Logger.Info("pipeline: detected Go module", "module", modPath)
		}
		if crate, err := cargo.ParseCrateName(opts.RepoPath); err == nil && crate != "" {
			rctx.RustCrateName = crate
			opts.Logger.Info("pipeline: detected Rust crate", "crate", crate)
		}
		b := graph.NewBuilder(nil, graph.Options{ResolverContext: rctx})
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

	// Phase: health. Weave hotspot paths + co-change partners + graph
	// edges into the analyzer so cross-cutting biomarkers (untested_hotspot,
	// hidden_coupling) fire.
	if err := runPhase(ctx, opts, store, PhaseHealth, res, func() error {
		analyzer := &health.Analyzer{
			// Duplication biomarker reads source bytes lazily under
			// the repo root. Closing over opts.RepoPath keeps the
			// analyzer agnostic of the pipeline's I/O strategy.
			SourceLoader: func(rel string) ([]byte, error) {
				return readFile(filepath.Join(opts.RepoPath, rel))
			},
		}
		if len(res.GitRecords) > 0 {
			hot := make(map[string]struct{}, len(res.GitRecords))
			coChange := make(map[string][]string, len(res.GitRecords))
			const minCoChange = 3
			for _, gr := range res.GitRecords {
				if gr.IsHotspot {
					hot[gr.Path] = struct{}{}
				}
				for _, p := range gr.CoChangePartners {
					if p.Count >= minCoChange {
						coChange[gr.Path] = append(coChange[gr.Path], p.Path)
					}
				}
			}
			analyzer.HotspotPaths = hot
			if len(coChange) > 0 {
				analyzer.CoChangePairs = coChange
			}
		}
		if res.Graph != nil {
			edges := map[string]map[string]bool{}
			for _, e := range res.Graph.Edges() {
				if edges[e.F.StringID] == nil {
					edges[e.F.StringID] = map[string]bool{}
				}
				edges[e.F.StringID][e.T.StringID] = true
			}
			analyzer.GraphEdges = edges
		}
		// Imported coverage (if any) makes untested_hotspot authoritative.
		if opts.DB != nil && opts.RepositoryID != "" {
			if cov, err := coveragestore.New(opts.DB).CoverageMap(ctx, opts.RepositoryID); err == nil && len(cov) > 0 {
				analyzer.Coverage = cov
			}
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

	// Phase: decisions. Runs every registered Extractor (inline_marker
	// in Pass A; ADR / CHANGELOG / commits in Pass B+) and collects
	// records de-duplicated by source + evidence location.
	if err := runPhase(ctx, opts, store, PhaseDecisions, res, func() error {
		res.Decisions = decisions.Engine{}.Run(ctx, decisions.Input{
			RepoRoot:    opts.RepoPath,
			ParsedFiles: res.Parsed,
		})
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
		if err := decisionstore.New(opts.DB).ReplaceAll(ctx, opts.RepositoryID, res.Decisions); err != nil {
			return fmt.Errorf("decisions: %w", err)
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
	job, err := store.Begin(ctx, opts.RepositoryID, phase, res.RunID)
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

// sha256Hex returns the hex-encoded SHA-256 of data — the parse-cache key.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// newRunID generates a fresh run_id. Format mirrors pipelinestore.newID
// (32 hex chars) so log readers can recognise the shape.
func newRunID() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")
}

// AbsRepoPath resolves a possibly-relative path to absolute. Surface so
// callers don't need to import path/filepath just for this.
func AbsRepoPath(p string) (string, error) {
	return filepath.Abs(p)
}
