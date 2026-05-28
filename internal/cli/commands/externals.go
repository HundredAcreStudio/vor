package commands

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newExternalsCmd prints external_systems rows.
func newExternalsCmd() *cobra.Command {
	var (
		repoPath  string
		repoID    string
		ecosystem string
		devOnly   bool
		limit     int
	)
	cmd := &cobra.Command{
		Use:   "externals",
		Short: "Show declared third-party dependencies",
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

			query := `SELECT name, ecosystem, COALESCE(version,''), declared_in, is_dev_dep
			          FROM external_systems WHERE repository_id = ?`
			qargs := []any{repoRow.ID}
			if ecosystem != "" {
				query += " AND ecosystem = ?"
				qargs = append(qargs, ecosystem)
			}
			if devOnly {
				query += " AND is_dev_dep = 1"
			}
			query += " ORDER BY ecosystem, name LIMIT ?"
			qargs = append(qargs, limit)

			rows, err := conn.QueryContext(ctx, query, qargs...)
			if err != nil {
				return fmt.Errorf("query: %w", err)
			}
			defer rows.Close()

			out := cmd.OutOrStdout()
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  ecosystem\tname\tversion\tdev\tdeclared_in")
			count := 0
			for rows.Next() {
				var (
					name, eco, version, declaredIn string
					isDev                          int
				)
				if err := rows.Scan(&name, &eco, &version, &declaredIn, &isDev); err != nil {
					return err
				}
				devFlag := ""
				if isDev == 1 {
					devFlag = "dev"
				}
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", eco, name, version, devFlag, declaredIn)
				count++
			}
			if count == 0 {
				fmt.Fprintln(out, "no external_systems rows (run `repowise ingest --persist`)")
			}
			return rows.Err()
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	cmd.Flags().StringVar(&ecosystem, "ecosystem", "", "filter by ecosystem (npm|pypi|cargo|go|nuget)")
	cmd.Flags().BoolVar(&devOnly, "dev", false, "show only dev/test dependencies")
	cmd.Flags().IntVar(&limit, "limit", 200, "max records to show")
	return cmd
}
