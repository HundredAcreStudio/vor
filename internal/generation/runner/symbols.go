package runner

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	gctx "github.com/HundredAcreStudio/vor/internal/generation/context"
	"github.com/HundredAcreStudio/vor/internal/generation/models"
	"github.com/HundredAcreStudio/vor/internal/generation/pages"
	"github.com/HundredAcreStudio/vor/internal/persistence/wikistore"
	"github.com/HundredAcreStudio/vor/internal/providers"
)

// runSymbolDetails iterates every non-file graph node with a name and
// produces a PageKindSymbolDetail per symbol. Source spans are sliced
// out of the file on disk; missing files mark the symbol StatusMissing.
//
// The caller list is derived from inbound graph edges; callees from
// outbound. Both are capped at maxNeighbors to keep prompts bounded.
const maxNeighbors = 25

func runSymbolDetails(ctx context.Context, opts Options, plan incrementalPlan, store *wikistore.Store, summary *Summary) error {
	symbols, err := loadIndexedSymbols(ctx, opts.DB, opts.RepositoryID, opts.Target)
	if err != nil {
		return fmt.Errorf("load symbols: %w", err)
	}
	if len(symbols) == 0 {
		return nil
	}

	gen := &pages.SymbolDetailGenerator{
		Provider: opts.Provider, Store: store, Model: opts.Model,
	}

	// Cache file reads — many symbols share a file.
	contents := map[string]string{}

	for _, s := range symbols {
		if opts.Limit > 0 && summary.GeneratedCount+summary.DryRunCount >= opts.Limit {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Incremental: only regenerate symbols whose host file changed.
		if plan.incremental && !plan.files[s.filePath] {
			continue
		}
		res := FileResult{Path: s.id}

		// Read the host file (cached).
		body, ok := contents[s.filePath]
		if !ok {
			data, err := os.ReadFile(filepath.Join(opts.RepoRoot, s.filePath))
			if err != nil {
				res.Status = StatusMissing
				res.Reason = fmt.Sprintf("host file not on disk: %v", err)
				recordResult(opts, summary, res)
				continue
			}
			body = string(data)
			contents[s.filePath] = body
		}

		span := gctx.SliceLines(body, s.startLine, s.endLine)
		callers, err := loadNeighbors(ctx, opts.DB, opts.RepositoryID, s.id, true)
		if err != nil {
			res.Status = StatusError
			res.Reason = err.Error()
			recordResult(opts, summary, res)
			continue
		}
		callees, _ := loadNeighbors(ctx, opts.DB, opts.RepositoryID, s.id, false)

		bundle := gctx.NewSymbolBundle(
			opts.RepoRoot, s.id, s.name, s.qualified, s.kind, s.filePath, s.language,
			s.startLine, s.endLine, span, callers, callees,
		)

		if !opts.Force {
			if existing, err := store.GetByTarget(ctx, opts.RepositoryID, models.PageKindSymbolDetail, s.id); err == nil {
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
			res.Reason = fmt.Sprintf("would generate (%d lines, hash %s…)", s.endLine-s.startLine+1, bundle.SourceHash[:8])
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

// indexedSymbol is the per-row projection used by runSymbolDetails.
type indexedSymbol struct {
	id, name, qualified, kind, filePath, language string
	startLine, endLine                            int
}

func loadIndexedSymbols(ctx context.Context, conn *sql.DB, repoID, target string) ([]indexedSymbol, error) {
	query := `
		SELECT node_id, COALESCE(name, ''), COALESCE(qualified_name, ''),
		       COALESCE(kind, ''), COALESCE(file_path, ''), COALESCE(language, ''),
		       COALESCE(start_line, 0), COALESCE(end_line, 0)
		FROM graph_nodes
		WHERE repository_id = ? AND node_type != 'file' AND name != ''`
	args := []any{repoID}
	if target != "" {
		query += " AND node_id = ?"
		args = append(args, target)
	}
	query += " ORDER BY file_path, start_line, node_id"
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []indexedSymbol{}
	for rows.Next() {
		var s indexedSymbol
		if err := rows.Scan(&s.id, &s.name, &s.qualified, &s.kind,
			&s.filePath, &s.language, &s.startLine, &s.endLine); err != nil {
			return nil, err
		}
		if s.filePath == "" {
			continue
		}
		if s.qualified == "" {
			s.qualified = s.name
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// loadNeighbors returns the names of nodes connected to nodeID via
// graph edges. inbound=true returns sources (callers); inbound=false
// returns targets (callees).
func loadNeighbors(ctx context.Context, conn *sql.DB, repoID, nodeID string, inbound bool) ([]string, error) {
	var query string
	if inbound {
		query = `
			SELECT COALESCE(s.name, s.node_id)
			FROM graph_edges e
			JOIN graph_nodes s ON s.repository_id = e.repository_id AND s.node_id = e.source_node_id
			WHERE e.repository_id = ? AND e.target_node_id = ?
			LIMIT ?`
	} else {
		query = `
			SELECT COALESCE(t.name, t.node_id)
			FROM graph_edges e
			JOIN graph_nodes t ON t.repository_id = e.repository_id AND t.node_id = e.target_node_id
			WHERE e.repository_id = ? AND e.source_node_id = ?
			LIMIT ?`
	}
	rows, err := conn.QueryContext(ctx, query, repoID, nodeID, maxNeighbors)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, rows.Err()
}
