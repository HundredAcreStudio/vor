package commands

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

// newDecisionsCmd shows decision_records from the persisted DB. Mirrors
// the read-only shape of health / dead-code / hotspots — no re-ingest.
func newDecisionsCmd() *cobra.Command {
	var (
		repoPath string
		limit    int
		source   string
	)
	cmd := &cobra.Command{
		Use:   "decisions",
		Short: "Show architectural decisions extracted from the codebase",
		Long: `Lists decision_records from the most recent ingest. Sources
include inline-marker comments (DECISION:, WHY:, TRADEOFF:) and,
in future passes, ADR files, CHANGELOG entries, and significant
git commits.`,
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

			query := `SELECT title, source, COALESCE(evidence_file,''),
			                COALESCE(evidence_line,0), confidence, verification
			          FROM decision_records WHERE repository_id = ?`
			qargs := []any{repoRow.ID}
			if source != "" {
				query += " AND source = ?"
				qargs = append(qargs, source)
			}
			query += " ORDER BY confidence DESC, evidence_file, evidence_line LIMIT ?"
			qargs = append(qargs, limit)

			rows, err := conn.QueryContext(ctx, query, qargs...)
			if err != nil {
				return fmt.Errorf("query: %w", err)
			}
			defer rows.Close()

			out := cmd.OutOrStdout()
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  conf\tsource\tlocation\ttitle")

			count := 0
			for rows.Next() {
				var (
					title, src, file, verification string
					line                            int
					conf                            float64
				)
				if err := rows.Scan(&title, &src, &file, &line, &conf, &verification); err != nil {
					return err
				}
				loc := file
				if line > 0 {
					loc = fmt.Sprintf("%s:%d", file, line)
				}
				fmt.Fprintf(tw, "  %.2f\t%s\t%s\t%s\n", conf, src, loc, title)
				count++
			}
			if count == 0 {
				fmt.Fprintln(out, "no decisions found (run `repowise init` first)")
			}
			return rows.Err()
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().IntVar(&limit, "limit", 50, "max records to show")
	cmd.Flags().StringVar(&source, "source", "", "filter by source (inline_marker|adr|changelog|git_archaeology)")
	return cmd
}
