package commands

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/persistence/healthstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

// newHealthCmd reads the persisted health_findings + health_file_metrics
// tables and prints a focused report. Read-only — no re-ingest.
func newHealthCmd() *cobra.Command {
	var (
		repoPath string
		limit    int
	)
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Show the code health report (read-only)",
		Long: `Reports the average code health score, per-biomarker finding counts,
and the worst-scoring files from the last persisted ingestion. Use
'repowise ingest --persist' first to populate the data.`,
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
			store := healthstore.New(conn)

			avg, err := store.AverageScore(ctx, repoRow.ID)
			if err != nil {
				return fmt.Errorf("AverageScore: %w", err)
			}
			total, err := store.CountFindings(ctx, repoRow.ID)
			if err != nil {
				return fmt.Errorf("CountFindings: %w", err)
			}
			byKind, err := store.CountByBiomarker(ctx, repoRow.ID)
			if err != nil {
				return fmt.Errorf("CountByBiomarker: %w", err)
			}

			out := cmd.OutOrStdout()
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

			rows, err := conn.QueryContext(ctx,
				`SELECT file_path, score, max_ccn, max_nesting, nloc
				 FROM health_file_metrics WHERE repository_id = ?
				 ORDER BY score ASC LIMIT ?`, repoRow.ID, limit)
			if err != nil {
				return fmt.Errorf("query worst files: %w", err)
			}
			defer rows.Close()

			fmt.Fprintln(out, "\nWorst-scoring files:")
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  score\tmaxCCN\tnesting\tnloc\tpath")
			for rows.Next() {
				var (
					path                                string
					score                               float64
					maxCCN, maxNesting, nloc            int
				)
				if err := rows.Scan(&path, &score, &maxCCN, &maxNesting, &nloc); err != nil {
					return err
				}
				fmt.Fprintf(tw, "  %.2f\t%d\t%d\t%d\t%s\n", score, maxCCN, maxNesting, nloc, path)
			}
			return rows.Err()
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().IntVar(&limit, "limit", 20, "max worst-scoring files to show")
	return cmd
}
