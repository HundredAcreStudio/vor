package commands

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newHotspotsCmd prints git_metadata hotspots from the persisted DB.
func newHotspotsCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
		limit    int
		all      bool
	)
	cmd := &cobra.Command{
		Use:   "hotspots",
		Short: "Show git hotspots (high-churn files) from the persisted ingestion",
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

			query := `SELECT file_path, churn_percentile, commit_count_total, commit_count_90d,
			                 COALESCE(primary_owner_name,''), bus_factor, contributor_count,
			                 lines_added_90d, lines_deleted_90d, is_hotspot
			          FROM git_metadata WHERE repository_id = ?`
			qargs := []any{repoRow.ID}
			if !all {
				query += " AND is_hotspot = 1"
			}
			query += " ORDER BY churn_percentile DESC, file_path LIMIT ?"
			qargs = append(qargs, limit)

			rows, err := conn.QueryContext(ctx, query, qargs...)
			if err != nil {
				return fmt.Errorf("query: %w", err)
			}
			defer rows.Close()

			out := cmd.OutOrStdout()
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  pctl\thot\tcommits\t90d\towner\tbusFactor\t+lines/-lines\tpath")

			count := 0
			for rows.Next() {
				var (
					path, owner                                       string
					pctl                                              float64
					commits, c90, busFactor, contributors, adds, dels int
					hotInt                                            int
				)
				if err := rows.Scan(&path, &pctl, &commits, &c90, &owner, &busFactor, &contributors, &adds, &dels, &hotInt); err != nil {
					return err
				}
				flag := ""
				if hotInt == 1 {
					flag = "HOT"
				}
				fmt.Fprintf(tw, "  %.2f\t%s\t%d\t%d\t%s\tbus=%d/%d\t+%d/-%d\t%s\n",
					pctl, flag, commits, c90, owner, busFactor, contributors, adds, dels, path)
				count++
			}
			if count == 0 {
				if all {
					fmt.Fprintln(out, "no git_metadata rows (run `vor ingest --persist` to populate)")
				} else {
					fmt.Fprintln(out, "no hotspot files (try --all to see every tracked file)")
				}
			}
			return rows.Err()
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	cmd.Flags().IntVar(&limit, "limit", 20, "max files to show")
	cmd.Flags().BoolVar(&all, "all", false, "show all tracked files, not just hotspots")
	return cmd
}
