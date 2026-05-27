package commands

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/persistence/healthstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

// newHealthCmd reads the persisted health_findings + health_file_metrics
// tables and prints a focused report. Read-only — no re-ingest.
//
// Flag parity with the Python implementation (lightly adapted):
//   --file        path filter (substring match)
//   --module      module/dir prefix filter (e.g. "internal/foo")
//   --refactoring-targets
//                 alternative view ranking files by impact/effort
//                 instead of raw worst score
//   --trend       last 10 snapshots (snapshot table TODO; returns a
//                 clear "not yet implemented" message)
//   --coverage    .lcov ingest for untested-hotspot signal (TODO)
func newHealthCmd() *cobra.Command {
	var (
		repoPath           string
		limit              int
		fileFilter         string
		moduleFilter       string
		refactoringTargets bool
		trend              bool
		coverageFiles      []string
	)
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Show the code health report (read-only)",
		Long: `Reports the average code health score, per-biomarker finding counts,
and the worst-scoring files from the last persisted ingestion. Use
'repowise init' (or 'repowise ingest --persist') first to populate
the data.

Filter with --file or --module to narrow the report to a subtree.
Use --refactoring-targets for an effort-weighted ranking.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, absRepoPath(repoPath), "")
			if err != nil {
				return fmt.Errorf("ensure repo: %w", err)
			}
			out := cmd.OutOrStdout()

			if trend {
				fmt.Fprintln(out,
					"trend view is not yet implemented in the Go port "+
						"(needs a health_score_snapshots table)")
				return nil
			}
			if len(coverageFiles) > 0 {
				fmt.Fprintln(out,
					"coverage ingestion is not yet implemented in the Go port "+
						"(would parse .lcov + light up untested_hotspot)")
				// Don't early-return; user might combine --coverage with
				// other flags to see today's report anyway.
			}

			store := healthstore.New(conn)
			avg, err := store.AverageScore(ctx, repoRow.ID)
			if err != nil {
				return err
			}
			total, err := store.CountFindings(ctx, repoRow.ID)
			if err != nil {
				return err
			}
			byKind, err := store.CountByBiomarker(ctx, repoRow.ID)
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "Code health: avg %.2f / 10 across all files\n", avg)
			fmt.Fprintf(out, "%d findings total\n", total)
			if len(byKind) > 0 {
				kinds := make([]string, 0, len(byKind))
				for k := range byKind {
					kinds = append(kinds, k)
				}
				sort.Strings(kinds)
				tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				for _, k := range kinds {
					fmt.Fprintf(tw, "  %s\t%d\n", k, byKind[k])
				}
				tw.Flush()
			}

			if refactoringTargets {
				return printRefactoringTargets(ctx, conn, out, repoRow.ID, limit, fileFilter, moduleFilter)
			}
			return printWorstFiles(ctx, conn, out, repoRow.ID, limit, fileFilter, moduleFilter)
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().IntVar(&limit, "limit", 20, "max rows to show")
	cmd.Flags().StringVar(&fileFilter, "file", "", "filter to file paths containing this substring")
	cmd.Flags().StringVar(&moduleFilter, "module", "", "filter to file paths under this directory prefix")
	cmd.Flags().BoolVar(&refactoringTargets, "refactoring-targets", false,
		"rank by impact/effort instead of raw worst score")
	cmd.Flags().BoolVar(&trend, "trend", false, "show health score trend (TODO: snapshot table)")
	cmd.Flags().StringSliceVar(&coverageFiles, "coverage", nil,
		"lcov coverage file(s) to ingest (TODO: coverage parser)")
	return cmd
}

// printWorstFiles is the default ranking — lowest score first.
func printWorstFiles(ctx context.Context, conn *sql.DB, out interface {
	Write(p []byte) (int, error)
}, repoID string, limit int, fileFilter, moduleFilter string) error {
	query := `SELECT file_path, score, max_ccn, max_nesting, nloc
	          FROM health_file_metrics WHERE repository_id = ?`
	args := []any{repoID}
	query, args = applyHealthFilters(query, args, fileFilter, moduleFilter, "file_path")
	query += " ORDER BY score ASC LIMIT ?"
	args = append(args, limit)

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Fprintln(out, "\nWorst-scoring files:")
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "  score\tmaxCCN\tnesting\tnloc\tpath")
	for rows.Next() {
		var (
			path                     string
			score                    float64
			maxCCN, maxNesting, nloc int
		)
		if err := rows.Scan(&path, &score, &maxCCN, &maxNesting, &nloc); err != nil {
			return err
		}
		fmt.Fprintf(tw, "  %.2f\t%d\t%d\t%d\t%s\n", score, maxCCN, maxNesting, nloc, path)
	}
	return rows.Err()
}

// printRefactoringTargets ranks by impact/effort. We use the sum of
// per-file finding HealthImpact divided by NLOC as a proxy for
// "biggest improvement per line touched". High-impact, small-file
// targets bubble to the top — they're cheapest to refactor.
func printRefactoringTargets(ctx context.Context, conn *sql.DB, out interface {
	Write(p []byte) (int, error)
}, repoID string, limit int, fileFilter, moduleFilter string) error {
	query := `
		SELECT m.file_path, m.score, m.nloc,
		       COALESCE(SUM(f.health_impact), 0) AS total_impact,
		       COUNT(f.id) AS finding_count
		FROM health_file_metrics m
		LEFT JOIN health_findings f
		  ON f.repository_id = m.repository_id AND f.file_path = m.file_path
		WHERE m.repository_id = ?`
	args := []any{repoID}
	query, args = applyHealthFilters(query, args, fileFilter, moduleFilter, "m.file_path")
	// Effort proxy: NLOC. Avoid division by zero.
	query += `
		GROUP BY m.file_path, m.score, m.nloc
		HAVING total_impact > 0
		ORDER BY (total_impact / (1 + m.nloc / 50.0)) DESC, m.score ASC
		LIMIT ?`
	args = append(args, limit)

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Fprintln(out, "\nRefactoring targets (impact / effort):")
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "  impact\teffort(nloc)\tscore\tfindings\tpath")
	for rows.Next() {
		var (
			path           string
			score, impact  float64
			nloc, findings int
		)
		if err := rows.Scan(&path, &score, &nloc, &impact, &findings); err != nil {
			return err
		}
		fmt.Fprintf(tw, "  %.2f\t%d\t%.2f\t%d\t%s\n", impact, nloc, score, findings, path)
	}
	return rows.Err()
}

// applyHealthFilters appends the --file / --module WHERE clauses if set.
// pathCol is the qualified column name (e.g. "m.file_path" inside a join).
func applyHealthFilters(query string, args []any, fileFilter, moduleFilter, pathCol string) (string, []any) {
	if fileFilter != "" {
		query += " AND " + pathCol + " LIKE ?"
		args = append(args, "%"+fileFilter+"%")
	}
	if moduleFilter != "" {
		mod := strings.TrimRight(moduleFilter, "/")
		query += " AND (" + pathCol + " = ? OR " + pathCol + " LIKE ?)"
		args = append(args, mod, mod+"/%")
	}
	return query, args
}
