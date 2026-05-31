package runner

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"sort"
	"strings"

	gctx "github.com/HundredAcreStudio/vor/internal/generation/context"
	"github.com/HundredAcreStudio/vor/internal/generation/models"
	"github.com/HundredAcreStudio/vor/internal/generation/pages"
	"github.com/HundredAcreStudio/vor/internal/persistence/wikistore"
	"github.com/HundredAcreStudio/vor/internal/providers"
)

// runDirectoryOverviews iterates every directory that contains at least
// one indexed file and generates a PageKindDirectoryOverview for it.
//
// Directories aren't stored as graph rows — we derive them from the
// distinct parent paths of file-typed graph_nodes. Same dispatch shape
// as runFileOverviews so behaviour stays predictable.
func runDirectoryOverviews(ctx context.Context, opts Options, store *wikistore.Store, summary *Summary) error {
	dirs, err := loadIndexedDirectories(ctx, opts.DB, opts.RepositoryID, opts.Target)
	if err != nil {
		return fmt.Errorf("load directories: %w", err)
	}
	if len(dirs) == 0 {
		return nil
	}

	// Pull existing file_overview summaries up-front so we can drop them
	// into each directory bundle without N+1 queries.
	fileSummaries, err := loadFileSummaries(ctx, opts.DB, opts.RepositoryID)
	if err != nil {
		return fmt.Errorf("load file summaries: %w", err)
	}

	gen := &pages.DirectoryOverviewGenerator{
		Provider: opts.Provider, Store: store, Model: opts.Model,
	}

	for _, d := range dirs {
		if opts.Limit > 0 && summary.GeneratedCount+summary.DryRunCount >= opts.Limit {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		summaries := map[string]string{}
		for _, child := range d.Children {
			if s, ok := fileSummaries[child]; ok {
				summaries[child] = s
			}
		}
		bundle := gctx.NewDirectoryBundle(opts.RepoRoot, d.Path, d.Children, d.Subdirs, summaries, d.DominantLanguage)

		res := FileResult{Path: displayDirPath(d.Path)}
		if !opts.Force {
			if existing, err := store.GetByTarget(ctx, opts.RepositoryID, models.PageKindDirectoryOverview, d.Path); err == nil {
				if existing.SourceHash == bundle.SourceHash {
					res.Status = StatusSkipped
					res.Reason = fmt.Sprintf("fresh page exists (v%d, %s)", existing.Version, existing.ModelName)
					p := existing
					res.Page = &p
					recordResult(opts, summary, res)
					continue
				}
			}
		}
		if opts.DryRun {
			res.Status = StatusDryRun
			res.Reason = fmt.Sprintf("would generate (%d children, hash %s…)", len(d.Children), bundle.SourceHash[:8])
			recordResult(opts, summary, res)
			continue
		}

		page, err := gen.Generate(ctx, opts.RepositoryID, bundle)
		if err != nil {
			res.Status = StatusError
			res.Reason = err.Error()
			res.Err = err
			recordResult(opts, summary, res)
			if providers.IsFatal(err) {
				return err
			}
			continue
		}
		res.Status = StatusGenerated
		res.Page = &page
		res.InputTokens = page.InputTokens
		res.OutputTokens = page.OutputTokens
		res.CachedTokens = page.CachedTokens
		recordResult(opts, summary, res)
	}
	return nil
}

// directoryInfo is the per-directory projection used by Pass D.
type directoryInfo struct {
	Path             string
	Children         []string
	Subdirs          []string
	DominantLanguage string
}

// loadIndexedDirectories enumerates every directory containing indexed
// files for the repo. The empty directory ("" — repo root) is included
// when there are root-level files. When target is non-empty, only the
// directory matching target (exactly) is returned.
func loadIndexedDirectories(ctx context.Context, conn *sql.DB, repoID, target string) ([]directoryInfo, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT node_id, COALESCE(file_path, ''), COALESCE(language, '')
		FROM graph_nodes
		WHERE repository_id = ? AND node_type = 'file'
	`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type dirAgg struct {
		children []string
		langs    map[string]int
	}
	dirs := map[string]*dirAgg{}
	allDirs := map[string]struct{}{}
	for rows.Next() {
		var nodeID, filePath, language string
		if err := rows.Scan(&nodeID, &filePath, &language); err != nil {
			return nil, err
		}
		// Older ingest paths leave file_path empty and stash the path
		// in node_id. Fall back so we don't silently drop everything.
		if filePath == "" {
			filePath = nodeID
		}
		if filePath == "" {
			continue
		}
		filePath = strings.ReplaceAll(filePath, "\\", "/")
		dir := path.Dir(filePath)
		if dir == "." {
			dir = ""
		}
		if _, ok := dirs[dir]; !ok {
			dirs[dir] = &dirAgg{langs: map[string]int{}}
		}
		dirs[dir].children = append(dirs[dir].children, filePath)
		if language != "" {
			dirs[dir].langs[language]++
		}
		// Record every ancestor (normalised; "." → "") so subdir lists
		// can be derived without inserting a phantom "." entry.
		allDirs[dir] = struct{}{}
		for p := dir; p != ""; {
			parent := path.Dir(p)
			if parent == "." {
				allDirs[""] = struct{}{}
				break
			}
			allDirs[parent] = struct{}{}
			p = parent
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Enumerate every directory — both those with direct files and pure
	// container directories (only subdirs). Container dirs are
	// architecturally meaningful too (e.g. "internal/generation" that
	// holds only subpackages).
	//
	// The empty-string repo root is deliberately excluded: that level
	// belongs to a future architecture page kind, and the wikistore
	// requires a non-empty TargetPath anyway.
	out := make([]directoryInfo, 0, len(allDirs))
	for d := range allDirs {
		if d == "" {
			continue
		}
		if target != "" && d != target {
			continue
		}
		var (
			children []string
			langs    map[string]int
		)
		if agg, ok := dirs[d]; ok {
			children = append([]string(nil), agg.children...)
			langs = agg.langs
		}
		info := directoryInfo{
			Path:             d,
			Children:         children,
			DominantLanguage: dominantLanguage(langs),
		}
		sort.Strings(info.Children)
		// Find direct subdirs: any d2 in allDirs whose parent is d.
		for cand := range allDirs {
			if cand == d {
				continue
			}
			parent := path.Dir(cand)
			if parent == "." {
				parent = ""
			}
			if parent == d {
				info.Subdirs = append(info.Subdirs, cand)
			}
		}
		sort.Strings(info.Subdirs)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// loadFileSummaries returns a map[file_path] = summary for every
// PageKindFileOverview row in the repo. Used so the directory prompt
// can quote existing summaries rather than re-summarising from scratch.
func loadFileSummaries(ctx context.Context, conn *sql.DB, repoID string) (map[string]string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT target_path, summary FROM wiki_pages
		WHERE repository_id = ? AND page_type = ?
	`, repoID, string(models.PageKindFileOverview))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var path, summary string
		if err := rows.Scan(&path, &summary); err != nil {
			return nil, err
		}
		out[path] = summary
	}
	return out, rows.Err()
}

func dominantLanguage(langs map[string]int) string {
	best := ""
	bestCount := 0
	for lang, n := range langs {
		if n > bestCount {
			best = lang
			bestCount = n
		}
	}
	return best
}

func displayDirPath(d string) string {
	if d == "" {
		return "(repo root)"
	}
	return d
}
