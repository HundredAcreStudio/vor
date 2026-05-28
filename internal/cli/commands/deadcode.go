package commands

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newDeadCodeCmd reads dead_code_findings from the persisted DB and
// prints them sorted by confidence descending.
func newDeadCodeCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
		limit    int
		safeOnly bool
	)
	cmd := &cobra.Command{
		Use:   "dead-code",
		Short: "Show unreachable files / symbols (read-only)",
		Long: `Reports dead_code_findings from the last persisted ingestion.
Defaults to all findings sorted by confidence; --safe-only filters
to findings the analyzer flagged SafeToDelete (confidence ≥ 0.9).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := resolveReadRepo(ctx, conn, readRepoOptions{Path: repoPath, ID: repoID})
			if err != nil {
				return err
			}

			query := `SELECT kind, file_path, COALESCE(symbol_name,''), COALESCE(symbol_kind,''),
			                 confidence, reason, safe_to_delete
			          FROM dead_code_findings WHERE repository_id = ?`
			qargs := []any{repoRow.ID}
			if safeOnly {
				query += " AND safe_to_delete = 1"
			}
			query += " ORDER BY confidence DESC, file_path LIMIT ?"
			qargs = append(qargs, limit)

			rows, err := conn.QueryContext(ctx, query, qargs...)
			if err != nil {
				return fmt.Errorf("query: %w", err)
			}
			defer rows.Close()

			out := cmd.OutOrStdout()
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  conf\tsafe\tkind\tpath\treason")

			count := 0
			for rows.Next() {
				var (
					kind, path, symbolName, symbolKind, reason string
					conf                                         float64
					safe                                         int
				)
				if err := rows.Scan(&kind, &path, &symbolName, &symbolKind, &conf, &reason, &safe); err != nil {
					return err
				}
				display := path
				if symbolName != "" {
					display = fmt.Sprintf("%s::%s", path, symbolName)
				}
				safeFlag := ""
				if safe == 1 {
					safeFlag = "SAFE"
				}
				fmt.Fprintf(tw, "  %.2f\t%s\t%s\t%s\t%s\n", conf, safeFlag, kind, display, reason)
				count++
			}
			if count == 0 {
				fmt.Fprintln(out, "no dead-code findings (run `repowise ingest --persist` to populate)")
			}
			return rows.Err()
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	cmd.Flags().IntVar(&limit, "limit", 50, "max findings to show")
	cmd.Flags().BoolVar(&safeOnly, "safe-only", false, "only show findings flagged SafeToDelete")
	return cmd
}
