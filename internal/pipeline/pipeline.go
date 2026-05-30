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

	"github.com/HundredAcreStudio/vor/internal/analysis/deadcode"
	"github.com/HundredAcreStudio/vor/internal/analysis/decisions"
	"github.com/HundredAcreStudio/vor/internal/analysis/health"
	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/ingestion/external"
	"github.com/HundredAcreStudio/vor/internal/ingestion/external/cargo"
	"github.com/HundredAcreStudio/vor/internal/ingestion/external/gomod"
	"github.com/HundredAcreStudio/vor/internal/ingestion/git"
	"github.com/HundredAcreStudio/vor/internal/ingestion/graph"
	"github.com/HundredAcreStudio/vor/internal/ingestion/graph/resolver"
	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser"
	"github.com/HundredAcreStudio/vor/internal/ingestion/traverser"

	// Side-effect imports — each per-language resolver registers itself
	// with the resolver registry in its init(). Add a language here as
	// soon as a resolver for it exists.
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/graph/resolver/cpp"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/graph/resolver/csharp"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/graph/resolver/golang"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/graph/resolver/java"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/graph/resolver/python"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/graph/resolver/rust"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/graph/resolver/typescript"

	// Side-effect imports — each decision-source extractor registers
	// itself with the decisions registry in its init(). Add a source
	// here as soon as its package exists.
	_ "github.com/HundredAcreStudio/vor/internal/analysis/decisions/adr"
	_ "github.com/HundredAcreStudio/vor/internal/analysis/decisions/changelog"
	_ "github.com/HundredAcreStudio/vor/internal/analysis/decisions/commits"
	_ "github.com/HundredAcreStudio/vor/internal/analysis/decisions/inline"

	"github.com/HundredAcreStudio/vor/internal/persistence/coveragestore"
	"github.com/HundredAcreStudio/vor/internal/persistence/deadstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/decisionstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/externalstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/gitstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/graphstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/healthstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/parsestore"
	"github.com/HundredAcreStudio/vor/internal/persistence/pipelinestore"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/persistence/snapshotstore"
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
	Files            []models.FileInfo
	Parsed           []models.ParsedFile
	Graph            *graph.Graph
	GitRecords       []git.PerFile
	CommitCategories map[string]int
	// GitReused is set when the git phase skipped the history walk because
	// HEAD was unchanged since the last index (commit-boundary task), reusing
	// the stored git_metadata. Persist then leaves the git tables untouched.
	GitReused bool
	DeadCode         []deadcode.Finding
	HealthResult     health.Result
	Externals        []external.Record
	Decisions        []decisions.Record
	TraversalStats   models.TraversalStats

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

	// Each phase runs in order through runPhase, which records its
	// start/complete/fail in pipeline_jobs. The git phase is best-effort:
	// a failure degrades gracefully rather than aborting the run.
	phases := []struct {
		name  string
		run   func() error
		fatal bool
	}{
		{PhaseTraverse, func() error { return phaseTraverse(ctx, opts, res) }, true},
		{PhaseParse, func() error { return phaseParse(ctx, opts, res) }, true},
		{PhaseGit, func() error { return phaseGit(ctx, opts, res) }, false},
		{PhaseGraph, func() error { return phaseGraph(ctx, opts, res) }, true},
		{PhaseDeadCode, func() error { return phaseDeadCode(res) }, true},
		{PhaseHealth, func() error { return phaseHealth(ctx, opts, res) }, true},
		{PhaseExternals, func() error { return phaseExternals(ctx, opts, res) }, true},
		{PhaseDecisions, func() error { return phaseDecisions(ctx, opts, res) }, true},
		{PhasePersist, func() error { return phasePersist(ctx, opts, res) }, true},
	}
	for _, p := range phases {
		if err := runPhase(ctx, opts, store, p.name, res, p.run); err != nil && p.fatal {
			return res, err
		}
	}
	return res, nil
}

// ---- Phases ---------------------------------------------------------------
// Each phase function performs one stage of the pipeline, reading/writing
// the shared *Result. They are invoked through runPhase (above), which
// owns the pipeline_jobs bookkeeping.

func phaseTraverse(ctx context.Context, opts Options, res *Result) error {
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
}

// phaseParse is incremental: files whose content hash + parser version
// match a cached entry skip the (expensive, cgo) tree-sitter parse and
// reuse the stored result. Changed/new files are re-parsed and cached;
// deleted files are pruned from the cache.
func phaseParse(ctx context.Context, opts Options, res *Result) error {
	ap := parser.New()
	useCache := opts.DB != nil && opts.RepositoryID != ""
	pstore := parsestore.New(opts.DB)

	var cache map[string]parsestore.Cached
	if useCache {
		cache, _ = pstore.LoadAll(ctx, opts.RepositoryID, parserVersion)
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

		if pf, ok := reuseCachedParse(cache, fi, h); ok {
			res.Parsed = append(res.Parsed, pf)
			stats.Reused++
			continue
		}

		pf, err := ap.Parse(ctx, fi, data)
		if err != nil {
			continue // tolerate per-file parse errors
		}
		res.Parsed = append(res.Parsed, pf)
		stats.Parsed++
		if e, ok := newCacheEntry(fi, h, pf); ok {
			dirty = append(dirty, e)
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
}

// reuseCachedParse returns the cached ParsedFile for fi when its hash
// matches, re-attaching the live FileInfo. ok=false on miss or decode error.
func reuseCachedParse(cache map[string]parsestore.Cached, fi models.FileInfo, hash string) (models.ParsedFile, bool) {
	c, ok := cache[fi.Path]
	if !ok || c.ContentHash != hash {
		return models.ParsedFile{}, false
	}
	var pf models.ParsedFile
	if json.Unmarshal(c.ParsedJSON, &pf) != nil {
		return models.ParsedFile{}, false
	}
	pf.FileInfo = fi
	return pf, true
}

// newCacheEntry serialises a freshly-parsed file for the cache, dropping
// the volatile FileInfo (paths/mtimes come from the live traversal on reload).
func newCacheEntry(fi models.FileInfo, hash string, pf models.ParsedFile) (parsestore.Entry, bool) {
	cp := pf
	cp.FileInfo = models.FileInfo{}
	b, err := json.Marshal(cp)
	if err != nil {
		return parsestore.Entry{}, false
	}
	return parsestore.Entry{Path: fi.Path, ContentHash: hash, ParsedJSON: b}, true
}

// phaseGit is best-effort — not all dirs are git repos. A failure logs and
// returns nil so the run continues.
func phaseGit(ctx context.Context, opts Options, res *Result) error {
	// Git signals are a commit-boundary task: they only change when HEAD
	// moves. When the current HEAD matches the commit we last indexed, skip
	// the (expensive) history walk and reuse the stored git_metadata — the
	// common case for working-tree edits that don't create a commit.
	if sha, _, err := git.ResolveHead(opts.RepoPath); err == nil && sha != "" && sha == storedHeadCommit(ctx, opts) {
		if recs, lerr := gitstore.New(opts.DB).LoadAll(ctx, opts.RepositoryID); lerr == nil && len(recs) > 0 {
			res.GitRecords = recs
			res.GitReused = true
			opts.Logger.Info("git: HEAD unchanged, reusing stored metadata", "commit", short(sha))
			return nil
		}
	}

	ix := &git.Indexer{MaxCommits: opts.GitMaxCommits}
	result, err := ix.IndexResult(ctx, opts.RepoPath)
	if err != nil {
		opts.Logger.Warn("git intelligence skipped", "err", err)
		return nil
	}
	res.GitRecords = result.Files
	res.CommitCategories = result.CommitCategories
	return nil
}

// storedHeadCommit returns the commit the repo was last indexed at (empty on
// first index or any lookup error).
func storedHeadCommit(ctx context.Context, opts Options) string {
	if opts.DB == nil || opts.RepositoryID == "" {
		return ""
	}
	if r, err := repos.New(opts.DB).Get(ctx, opts.RepositoryID); err == nil && r != nil {
		return r.HeadCommit
	}
	return ""
}

func short(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}

// phaseGraph builds the dependency graph using the per-language resolver
// registry, reading go.mod / Cargo.toml so resolvers can translate
// module-path imports into file paths.
func phaseGraph(_ context.Context, opts Options, res *Result) error {
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
	for _, pf := range res.Parsed {
		b.AddFile(pf)
	}
	g := b.Build()
	g.ComputeMetrics()
	res.Graph = g
	return nil
}

func phaseDeadCode(res *Result) error {
	res.DeadCode = (&deadcode.Analyzer{MinConfidence: 0.5}).Analyze(res.Graph)
	return nil
}

// phaseHealth weaves hotspot paths + co-change partners + graph edges +
// imported coverage into the analyzer so cross-cutting biomarkers fire.
func phaseHealth(ctx context.Context, opts Options, res *Result) error {
	analyzer := &health.Analyzer{
		SourceLoader: func(rel string) ([]byte, error) {
			return readFile(filepath.Join(opts.RepoPath, rel))
		},
		Exclude: healthExcludesFor(ctx, opts.DB, opts.RepositoryID),
	}
	if len(res.GitRecords) > 0 {
		analyzer.HotspotPaths, analyzer.CoChangePairs = hotspotsAndCoChanges(res.GitRecords)
	}
	if res.Graph != nil {
		analyzer.GraphEdges = graphEdgeSet(res.Graph)
	}
	if opts.DB != nil && opts.RepositoryID != "" {
		if cov, err := coveragestore.New(opts.DB).CoverageMap(ctx, opts.RepositoryID); err == nil && len(cov) > 0 {
			analyzer.Coverage = cov
		}
	}
	res.HealthResult = analyzer.Analyze(res.Parsed)
	return nil
}

// healthExcludesFor resolves the repo's config from the DB and projects its
// health_rules onto health.ExcludeRule, keeping only rules that actually
// disable a biomarker. A nil connection or any resolution error yields no
// exclusions (best-effort: health analysis should never fail because of a
// config typo or a not-yet-persisted run).
func healthExcludesFor(ctx context.Context, conn *sql.DB, repoID string) []health.ExcludeRule {
	if conn == nil {
		return nil
	}
	cfg, err := config.Resolve(ctx, conn, repoID, config.LoadBootstrap())
	if err != nil {
		return nil
	}
	var out []health.ExcludeRule
	for _, r := range cfg.HealthRules {
		var biomarkers []string
		for name, action := range r.Overrides {
			if isDisableAction(action) {
				biomarkers = append(biomarkers, name)
			}
		}
		if len(biomarkers) == 0 {
			continue // no disabling override — nothing to suppress
		}
		out = append(out, health.ExcludeRule{Pattern: r.Pattern, Path: r.Path, Biomarkers: biomarkers})
	}
	return out
}

// isDisableAction reports whether a health_rules override value turns a
// biomarker off. Other actions (e.g. a future "warn") are not yet wired.
func isDisableAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "disabled", "disable", "off", "skip", "ignore", "exclude", "none":
		return true
	}
	return false
}

// hotspotsAndCoChanges derives the hotspot set and the co-change adjacency
// (filtered to a minimum co-change count) from git records.
func hotspotsAndCoChanges(records []git.PerFile) (map[string]struct{}, map[string][]string) {
	const minCoChange = 3
	hat := make(map[string]struct{}, len(records))
	coChange := map[string][]string{}
	for _, gr := range records {
		if gr.IsHotspot {
			hat[gr.Path] = struct{}{}
		}
		for _, pr := range gr.CoChangePartners {
			if pr.Count >= minCoChange {
				coChange[gr.Path] = append(coChange[gr.Path], pr.Path)
			}
		}
	}
	if len(coChange) == 0 {
		return hat, nil
	}
	return hat, coChange
}

// graphEdgeSet flattens the graph's edges into a source→target adjacency
// set for the hidden_coupling biomarker.
func graphEdgeSet(g *graph.Graph) map[string]map[string]bool {
	edges := map[string]map[string]bool{}
	for _, e := range g.Edges() {
		if edges[e.F.StringID] == nil {
			edges[e.F.StringID] = map[string]bool{}
		}
		edges[e.F.StringID][e.T.StringID] = true
	}
	return edges
}

func phaseExternals(ctx context.Context, opts Options, res *Result) error {
	recs, err := external.ScanRoot(ctx, opts.RepoPath)
	if err != nil {
		return err
	}
	res.Externals = recs
	return nil
}

// phaseDecisions runs every registered decision-source Extractor and
// collects records de-duplicated by source + evidence location.
func phaseDecisions(ctx context.Context, opts Options, res *Result) error {
	res.Decisions = decisions.Engine{}.Run(ctx, decisions.Input{
		RepoRoot:    opts.RepoPath,
		ParsedFiles: res.Parsed,
	})
	return nil
}

// phasePersist writes every store (one ReplaceAll per table).
func phasePersist(ctx context.Context, opts Options, res *Result) error {
	repoStore := repos.New(opts.DB)
	sha, branch, _ := git.ResolveHead(opts.RepoPath)
	if sha != "" {
		_ = repoStore.UpdateHeadCommit(ctx, opts.RepositoryID, sha)
	}
	if err := graphstore.New(opts.DB).ReplaceGraph(ctx, opts.RepositoryID, res.Graph); err != nil {
		return fmt.Errorf("graph: %w", err)
	}
	// Skip the git tables when the phase reused stored metadata (HEAD
	// unchanged) — re-writing identical rows is pure churn.
	if !res.GitReused {
		if err := gitstore.New(opts.DB).ReplaceAll(ctx, opts.RepositoryID, res.GitRecords); err != nil {
			return fmt.Errorf("git_metadata: %w", err)
		}
		if err := gitstore.New(opts.DB).ReplaceCommitCategories(ctx, opts.RepositoryID, res.CommitCategories); err != nil {
			return fmt.Errorf("commit_categories: %w", err)
		}
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

	// Append a health snapshot for this commit (per-commit history; the
	// latest-only health tables get wiped each run, so snapshots are the
	// durable timeline). Best-effort — never fail the index over it.
	writeHealthSnapshot(ctx, opts, res, sha, branch)
	return nil
}

// writeHealthSnapshot appends one health_snapshots row per distinct indexed
// commit (skips when the latest snapshot already covers this commit). It
// aggregates the per-file health scores into average/hotspot/worst figures.
func writeHealthSnapshot(ctx context.Context, opts Options, res *Result, sha, branch string) {
	if sha == "" {
		return // no commit to key the snapshot on (not a git repo / detached pre-commit)
	}
	store := snapshotstore.New(opts.DB)
	if latest, err := store.Latest(ctx, opts.RepositoryID); err == nil && latest != nil && latest.CommitSHA == sha {
		return // already snapshotted this commit
	}

	scores := make(map[string]float64, len(res.HealthResult.FileMetrics))
	var sum float64
	worstPath, worstScore := "", 11.0
	for _, m := range res.HealthResult.FileMetrics {
		scores[m.FilePath] = m.Score
		sum += m.Score
		if m.Score < worstScore {
			worstScore, worstPath = m.Score, m.FilePath
		}
	}
	avg := 10.0
	if n := len(scores); n > 0 {
		avg = sum / float64(n)
	}

	// Hotspot health: average score over files flagged as hotspots.
	var hSum float64
	var hN int
	for _, g := range res.GitRecords {
		if g.IsHotspot {
			if s, ok := scores[g.Path]; ok {
				hSum += s
				hN++
			}
		}
	}
	hotspot := avg
	if hN > 0 {
		hotspot = hSum / float64(hN)
	}
	if worstPath == "" {
		worstScore = avg
	}

	_ = store.Insert(ctx, snapshotstore.Snapshot{
		RepositoryID:  opts.RepositoryID,
		CommitSHA:     sha,
		Branch:        branch,
		AverageHealth: avg,
		HotspotHealth: hotspot,
		WorstPath:     worstPath,
		WorstScore:    worstScore,
		PerFileScores: scores,
	})
	_ = store.Prune(ctx, opts.RepositoryID, 500)
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
