// Package runner orchestrates per-repo page generation. Reads the
// persisted ingest state (graph_nodes + git_metadata + dead_code_findings
// + health_findings), assembles a context bundle per file, hands it to a
// page Generator, and aggregates the results.
//
// This package is deliberately CLI-agnostic: the cobra command in
// internal/cli/commands/generate.go is a thin shell around Run().
package runner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	gctx "github.com/HundredAcreStudio/vor/internal/generation/context"
	"github.com/HundredAcreStudio/vor/internal/generation/models"
	"github.com/HundredAcreStudio/vor/internal/generation/pages"
	"github.com/HundredAcreStudio/vor/internal/persistence/wikistore"
	"github.com/HundredAcreStudio/vor/internal/providers"
)

// Options controls one generation pass.
type Options struct {
	// RepoRoot is the absolute path to the repository on disk. Required.
	RepoRoot string
	// RepositoryID is the row in repositories. Required.
	RepositoryID string
	// DB is the open *sql.DB. Required.
	DB *sql.DB
	// Provider is the LLM provider to call. Required.
	Provider providers.Provider
	// Model is the model identifier (passes through to Provider). Empty
	// means "let the provider pick".
	Model string

	// Kinds names which PageKinds this run should produce. Empty defaults
	// to [PageKindFileOverview] for backwards compatibility with the
	// Pass B CLI. Pass D adds DirectoryOverview and SymbolDetail.
	Kinds []models.PageKind

	// Target restricts generation to a single target path (repo-relative
	// file for file/directory kinds; symbol_id like "file.go::Sym" for
	// symbol_detail). Empty means "every indexed unit of the selected
	// Kinds".
	Target string
	// Limit caps the number of pages generated this run. 0 means "no cap".
	Limit int
	// Force regenerates even when the existing page's source_hash matches
	// the on-disk file (i.e. the page is fresh). Useful for prompt or
	// model swaps.
	Force bool
	// DryRun prints what would be generated without calling the provider.
	DryRun bool

	// OnProgress is called once per unit with its outcome. Optional;
	// when nil, the runner is silent. Useful for CLI tick output.
	OnProgress func(FileResult)
}

// FileResult is the outcome for one file's generation attempt.
type FileResult struct {
	Path         string
	Status       Status
	Reason       string // human-readable detail for skips/errors
	Err          error  // underlying error when Status==StatusError; nil otherwise
	Page         *models.Page
	InputTokens  int
	OutputTokens int
	CachedTokens int
}

// Status is the verdict per file.
type Status string

const (
	StatusGenerated Status = "generated"
	StatusSkipped   Status = "skipped" // fresh page already exists; --force overrides
	StatusDryRun    Status = "dry_run"
	StatusError     Status = "error"
	StatusMissing   Status = "missing" // graph_nodes points at a path that's no longer on disk
)

// Summary is the aggregate result of Run.
type Summary struct {
	Files             []FileResult
	GeneratedCount    int
	SkippedCount      int
	ErrorCount        int
	MissingCount      int
	DryRunCount       int
	TotalInputTokens  int
	TotalOutputTokens int
	TotalCachedTokens int
}

// Run executes one generation pass and returns the summary. Errors on
// any file are recorded in FileResult.Status=StatusError; the run
// continues. The top-level error is reserved for setup failures (bad
// options, DB query failure).
func Run(ctx context.Context, opts Options) (Summary, error) {
	if err := validate(opts); err != nil {
		return Summary{}, err
	}
	kinds := opts.Kinds
	if len(kinds) == 0 {
		kinds = []models.PageKind{models.PageKindFileOverview}
	}

	summary := Summary{Files: []FileResult{}}
	store := wikistore.New(opts.DB)

	for _, kind := range kinds {
		if opts.Limit > 0 && summary.GeneratedCount+summary.DryRunCount >= opts.Limit {
			break
		}
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		var (
			err error
		)
		switch kind {
		case models.PageKindFileOverview:
			err = runFileOverviews(ctx, opts, store, &summary)
		case models.PageKindDirectoryOverview:
			err = runDirectoryOverviews(ctx, opts, store, &summary)
		case models.PageKindSymbolDetail:
			err = runSymbolDetails(ctx, opts, store, &summary)
		case models.PageKindArchitecture:
			err = runArchitecture(ctx, opts, store, &summary)
		default:
			err = fmt.Errorf("runner: unsupported kind %q", kind)
		}
		if err != nil {
			return summary, err
		}
	}
	return summary, nil
}

// runFileOverviews handles the file_overview kind — extracted from the
// original Run for Pass D's multi-kind dispatch.
func runFileOverviews(ctx context.Context, opts Options, store *wikistore.Store, summary *Summary) error {
	files, err := loadIndexedFiles(ctx, opts.DB, opts.RepositoryID, opts.Target)
	if err != nil {
		return fmt.Errorf("load indexed files: %w", err)
	}
	if len(files) == 0 {
		return nil
	}
	gen := &pages.FileOverviewGenerator{
		Provider: opts.Provider, Store: store, Model: opts.Model,
	}
	for _, f := range files {
		if opts.Limit > 0 && summary.GeneratedCount+summary.DryRunCount >= opts.Limit {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		res := generateOne(ctx, opts, gen, store, f)
		recordResult(opts, summary, res)
		if providers.IsFatal(res.Err) {
			return res.Err
		}
	}
	return nil
}

func recordResult(opts Options, summary *Summary, res FileResult) {
	summary.Files = append(summary.Files, res)
	switch res.Status {
	case StatusGenerated:
		summary.GeneratedCount++
		summary.TotalInputTokens += res.InputTokens
		summary.TotalOutputTokens += res.OutputTokens
		summary.TotalCachedTokens += res.CachedTokens
	case StatusSkipped:
		summary.SkippedCount++
	case StatusError:
		summary.ErrorCount++
	case StatusMissing:
		summary.MissingCount++
	case StatusDryRun:
		summary.DryRunCount++
	}
	if opts.OnProgress != nil {
		opts.OnProgress(res)
	}
}

// generateOne handles a single file: read, hash, decide, call.
func generateOne(ctx context.Context, opts Options, gen *pages.FileOverviewGenerator, store *wikistore.Store, f indexedFile) FileResult {
	res := FileResult{Path: f.path}

	bundle, err := gctx.LoadFile(opts.RepoRoot, f.path, f.language)
	if err != nil {
		res.Status = StatusMissing
		res.Reason = fmt.Sprintf("file not on disk: %v", err)
		return res
	}
	bundle.Symbols = f.symbols

	// Skip when there's already a fresh page (same source_hash). --force
	// overrides this.
	if !opts.Force {
		if existing, err := store.GetByTarget(ctx, opts.RepositoryID, models.PageKindFileOverview, f.path); err == nil {
			if existing.SourceHash == bundle.SourceHash {
				res.Status = StatusSkipped
				res.Reason = fmt.Sprintf("fresh page exists (v%d, %s)", existing.Version, existing.ModelName)
				p := existing
				res.Page = &p
				return res
			}
		}
	}

	if opts.DryRun {
		res.Status = StatusDryRun
		res.Reason = fmt.Sprintf("would generate (%d bytes source, hash %s…)", len(bundle.Content), bundle.SourceHash[:8])
		return res
	}

	// Best-effort signal augmentation: pull per-file git + dead + health
	// rows. Errors here are tolerable — the bundle just won't carry
	// those annotations.
	bundle.Signals = loadSignals(ctx, opts.DB, opts.RepositoryID, f.path)

	page, err := gen.Generate(ctx, opts.RepositoryID, bundle)
	if err != nil {
		res.Status = StatusError
		res.Reason = err.Error()
		res.Err = err
		return res
	}
	res.Status = StatusGenerated
	res.Page = &page
	res.InputTokens = page.InputTokens
	res.OutputTokens = page.OutputTokens
	res.CachedTokens = page.CachedTokens
	return res
}

// indexedFile is the per-file projection of graph_nodes used by Run.
type indexedFile struct {
	path     string
	language string
	symbols  []string
}

// loadIndexedFiles returns every file-typed graph_node for the repo,
// joined with its top symbols (also from graph_nodes, where node_type
// is a symbol kind). Ordered by path for determinism.
func loadIndexedFiles(ctx context.Context, conn *sql.DB, repoID, target string) ([]indexedFile, error) {
	query := `
		SELECT DISTINCT node_id, COALESCE(language, ''), COALESCE(file_path, '')
		FROM graph_nodes
		WHERE repository_id = ? AND node_type = 'file'`
	args := []any{repoID}
	if target != "" {
		query += " AND (node_id = ? OR file_path = ?)"
		args = append(args, target, target)
	}
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []indexedFile{}
	for rows.Next() {
		var nodeID, language, filePath string
		if err := rows.Scan(&nodeID, &language, &filePath); err != nil {
			return nil, err
		}
		path := filePath
		if path == "" {
			path = nodeID
		}
		path = filepath.ToSlash(path)
		out = append(out, indexedFile{path: path, language: language})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Augment with per-file symbol lists. Done as one extra query so the
	// join above stays simple.
	if err := augmentSymbols(ctx, conn, repoID, out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// augmentSymbols loads symbol names from graph_nodes (rows with
// non-empty kind/name) and attaches up to N per file.
func augmentSymbols(ctx context.Context, conn *sql.DB, repoID string, files []indexedFile) error {
	if len(files) == 0 {
		return nil
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT COALESCE(file_path, ''), COALESCE(name, '')
		FROM graph_nodes
		WHERE repository_id = ? AND node_type != 'file' AND name != ''
	`, repoID)
	if err != nil {
		return err
	}
	defer rows.Close()
	byPath := map[string][]string{}
	for rows.Next() {
		var path, name string
		if err := rows.Scan(&path, &name); err != nil {
			return err
		}
		path = filepath.ToSlash(path)
		if path != "" {
			byPath[path] = append(byPath[path], name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	const maxPerFile = 12
	for i := range files {
		syms := byPath[files[i].path]
		if len(syms) > maxPerFile {
			syms = syms[:maxPerFile]
		}
		files[i].symbols = syms
	}
	return nil
}

// loadSignals pulls hotspot + dead-code + health-finding signals for one
// file. All failures are silenced — signals are optional context for the
// prompt, not load-bearing.
func loadSignals(ctx context.Context, conn *sql.DB, repoID, path string) gctx.Signals {
	s := gctx.Signals{}
	var ownerName, ownerEmail sql.NullString
	_ = conn.QueryRowContext(ctx, `
		SELECT is_hotspot, is_stable, commit_count_90d,
		       COALESCE(primary_owner_name, ''), COALESCE(primary_owner_email, '')
		FROM git_metadata WHERE repository_id = ? AND file_path = ?
	`, repoID, path).Scan(&s.IsHotspot, &s.IsStable, &s.CommitCount90d, &ownerName, &ownerEmail)
	if ownerName.Valid && ownerName.String != "" {
		s.PrimaryOwner = ownerName.String
		if ownerEmail.Valid && ownerEmail.String != "" {
			s.PrimaryOwner += " <" + ownerEmail.String + ">"
		}
	}
	_ = conn.QueryRowContext(ctx, `
		SELECT reason FROM dead_code_findings
		WHERE repository_id = ? AND file_path = ? LIMIT 1
	`, repoID, path).Scan(&s.DeadCodeReason)
	_ = conn.QueryRowContext(ctx, `
		SELECT biomarker_type FROM health_findings
		WHERE repository_id = ? AND file_path = ?
		ORDER BY health_impact DESC LIMIT 1
	`, repoID, path).Scan(&s.HealthBiomarker)
	return s
}

func validate(opts Options) error {
	if opts.RepoRoot == "" {
		return errors.New("runner: RepoRoot required")
	}
	if opts.RepositoryID == "" {
		return errors.New("runner: RepositoryID required")
	}
	if opts.DB == nil {
		return errors.New("runner: DB required")
	}
	if opts.Provider == nil && !opts.DryRun {
		return errors.New("runner: Provider required (unless DryRun)")
	}
	return nil
}
