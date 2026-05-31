package runner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	gctx "github.com/HundredAcreStudio/vor/internal/generation/context"
	"github.com/HundredAcreStudio/vor/internal/generation/models"
	"github.com/HundredAcreStudio/vor/internal/generation/pages"
	"github.com/HundredAcreStudio/vor/internal/insights"
	"github.com/HundredAcreStudio/vor/internal/persistence/decisionstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/healthstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/persistence/wikistore"
	"github.com/HundredAcreStudio/vor/internal/providers"
)

const (
	archMaxLanguages = 8
	archMaxModules   = 10
	archMaxEntry     = 8
	archMaxCommunity = 5
	archMaxDecisions = 8
)

// runArchitecture generates the single repo-wide overview page. It assembles a
// context bundle from existing analysis (languages, entry points, modules,
// communities, decisions) and hands it to the LLM. The page is skipped when a
// fresh one exists whose source hash matches the current structural inputs.
func runArchitecture(ctx context.Context, opts Options, store *wikistore.Store, summary *Summary) error {
	bundle, err := loadArchitectureBundle(ctx, opts.DB, opts.RepositoryID, opts.RepoRoot)
	if err != nil {
		return fmt.Errorf("assemble architecture context: %w", err)
	}

	res := FileResult{Path: "(repository overview)"}
	switch {
	case !opts.Force && archIsFresh(ctx, store, opts.RepositoryID, bundle.SourceHash, &res):
		// res populated as Skipped by archIsFresh.
	case opts.DryRun:
		res.Status = StatusDryRun
		res.Reason = fmt.Sprintf("would generate repo overview (%d modules, %d languages)",
			len(bundle.Modules), len(bundle.Languages))
	default:
		gen := &pages.ArchitectureGenerator{Provider: opts.Provider, Store: store, Model: opts.Model}
		page, gerr := gen.Generate(ctx, opts.RepositoryID, bundle)
		if gerr != nil {
			res.Status = StatusError
			res.Reason = gerr.Error()
			res.Err = gerr
		} else {
			res.Status = StatusGenerated
			res.Page = &page
			res.InputTokens = page.InputTokens
			res.OutputTokens = page.OutputTokens
			res.CachedTokens = page.CachedTokens
		}
	}
	recordResult(opts, summary, res)
	if providers.IsFatal(res.Err) {
		return res.Err
	}
	return nil
}

// archIsFresh reports whether a fresh architecture page already exists for the
// current source hash, populating res as Skipped when so.
func archIsFresh(ctx context.Context, store *wikistore.Store, repoID, hash string, res *FileResult) bool {
	existing, err := store.GetByTarget(ctx, repoID, models.PageKindArchitecture, pages.ArchitectureTargetPath)
	if err != nil || existing.SourceHash != hash {
		return false
	}
	res.Status = StatusSkipped
	res.Reason = fmt.Sprintf("fresh page exists (v%d, %s)", existing.Version, existing.ModelName)
	p := existing
	res.Page = &p
	return true
}

func loadArchitectureBundle(ctx context.Context, conn *sql.DB, repoID, repoRoot string) (gctx.ArchitectureBundle, error) {
	b := gctx.ArchitectureBundle{RepoName: archRepoName(ctx, conn, repoID, repoRoot)}

	if err := conn.QueryRowContext(ctx,
		`SELECT
		   COALESCE(SUM(CASE WHEN node_type = 'file' THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN node_type <> 'file' THEN 1 ELSE 0 END), 0)
		 FROM graph_nodes WHERE repository_id = ?`, repoID).Scan(&b.FileCount, &b.SymbolCount); err != nil {
		return gctx.ArchitectureBundle{}, fmt.Errorf("node counts: %w", err)
	}

	// Languages → tech-stack shares (best-effort; absent data just omits a
	// section rather than failing the whole page).
	if langs, err := insights.Languages(ctx, conn, repoID); err == nil && langs.Total > 0 {
		sort.Slice(langs.Languages, func(i, j int) bool { return langs.Languages[i].Files > langs.Languages[j].Files })
		for _, l := range langs.Languages {
			b.Languages = append(b.Languages, gctx.LangShare{
				Language: l.Language, Files: l.Files,
				Pct: 100 * float64(l.Files) / float64(langs.Total),
			})
			if len(b.Languages) >= archMaxLanguages {
				break
			}
		}
	}

	if mods, err := insights.Modules(ctx, conn, repoID); err == nil {
		sort.Slice(mods, func(i, j int) bool { return mods[i].Files > mods[j].Files })
		for _, m := range mods {
			b.Modules = append(b.Modules, gctx.ModuleShare{Name: m.Name, Files: m.Files, Symbols: m.Symbols})
			if len(b.Modules) >= archMaxModules {
				break
			}
		}
	}

	if eps, err := insights.EntryPoints(ctx, conn, repoID); err == nil {
		for _, e := range eps {
			b.EntryPoints = append(b.EntryPoints, e.Path)
			if len(b.EntryPoints) >= archMaxEntry {
				break
			}
		}
	}

	if comms, err := insights.Communities(ctx, conn, repoID); err == nil {
		b.CommunityCount = len(comms)
		sort.Slice(comms, func(i, j int) bool { return comms[i].Size > comms[j].Size })
		for i, c := range comms {
			if i >= archMaxCommunity {
				break
			}
			label := c.Label
			if label == "" && len(c.Top) > 0 {
				label = c.Top[0]
			}
			b.TopCommunities = append(b.TopCommunities, fmt.Sprintf("%s (%d files)", label, c.Size))
		}
	}

	if avg, err := healthstore.New(conn).AverageScore(ctx, repoID); err == nil {
		b.HealthAvg = avg
	}

	if n, err := decisionstore.New(conn).Count(ctx, repoID); err == nil {
		b.DecisionCount = n
	}
	b.TopDecisions = archTopDecisions(ctx, conn, repoID)

	b.SourceHash = archSourceHash(b)
	return b, nil
}

// archRepoName returns the repo's name, falling back to the directory base.
func archRepoName(ctx context.Context, conn *sql.DB, repoID, repoRoot string) string {
	if repo, err := repos.New(conn).Get(ctx, repoID); err == nil && repo != nil && repo.Name != "" {
		return repo.Name
	}
	if repoRoot != "" {
		return filepath.Base(repoRoot)
	}
	return "repository"
}

// archTopDecisions returns the titles of the highest-confidence recorded
// decisions. Best-effort: any error yields an empty list.
func archTopDecisions(ctx context.Context, conn *sql.DB, repoID string) []string {
	rows, err := conn.QueryContext(ctx,
		`SELECT title FROM decision_records WHERE repository_id = ?
		 ORDER BY confidence DESC LIMIT ?`, repoID, archMaxDecisions)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return out
		}
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// archSourceHash digests the structural inputs so the overview regenerates
// when the repo's shape changes (new languages, modules, entry points,
// circular-dependency clusters, or decisions) but is skipped otherwise.
func archSourceHash(b gctx.ArchitectureBundle) string {
	// Sort the component lines so the hash depends only on the set of inputs,
	// not on slice ordering (tied modules/languages/entry points can sort
	// differently run-to-run, which would otherwise force needless regen).
	lines := []string{fmt.Sprintf("f=%d;s=%d;cc=%d;dc=%d", b.FileCount, b.SymbolCount, b.CommunityCount, b.DecisionCount)}
	for _, l := range b.Languages {
		lines = append(lines, fmt.Sprintf("L:%s=%d", l.Language, l.Files))
	}
	for _, m := range b.Modules {
		lines = append(lines, fmt.Sprintf("M:%s=%d/%d", m.Name, m.Files, m.Symbols))
	}
	for _, e := range b.EntryPoints {
		lines = append(lines, "E:"+e)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}
