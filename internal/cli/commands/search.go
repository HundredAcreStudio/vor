package commands

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newSearchCmd searches the persisted graph_nodes by name (LIKE-based for
// simplicity; full FTS5 search lands when we populate page_fts in
// Phase 5).
func newSearchCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
		limit    int
		nodeType string
	)
	cmd := &cobra.Command{
		Use:   "search [QUERY]",
		Short: "Search persisted symbols by name",
		Args:  cobra.MinimumNArgs(1),
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

			query := strings.Join(args, " ")
			pattern := "%" + query + "%"

			sqlQ := `SELECT node_id, node_type, COALESCE(kind,''), COALESCE(name,''),
			                COALESCE(file_path,''), COALESCE(start_line,0), pagerank
			         FROM graph_nodes
			         WHERE repository_id = ?
			           AND (name LIKE ? OR qualified_name LIKE ? OR node_id LIKE ?)`
			qargs := []any{repoRow.ID, pattern, pattern, pattern}
			if nodeType != "" {
				sqlQ += " AND node_type = ?"
				qargs = append(qargs, nodeType)
			}
			sqlQ += " ORDER BY pagerank DESC, node_id LIMIT ?"
			qargs = append(qargs, limit)

			rows, err := conn.QueryContext(ctx, sqlQ, qargs...)
			if err != nil {
				return fmt.Errorf("query: %w", err)
			}
			defer rows.Close()

			out := cmd.OutOrStdout()
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  pagerank\tkind\tname\tfile:line\tnode_id")

			count := 0
			for rows.Next() {
				var (
					nodeID, ntype, kind, name, filePath string
					startLine                            int
					pagerank                             float64
				)
				if err := rows.Scan(&nodeID, &ntype, &kind, &name, &filePath, &startLine, &pagerank); err != nil {
					return err
				}
				location := filePath
				if startLine > 0 {
					location = fmt.Sprintf("%s:%d", filePath, startLine)
				}
				displayKind := kind
				if displayKind == "" {
					displayKind = ntype
				}
				fmt.Fprintf(tw, "  %.4f\t%s\t%s\t%s\t%s\n", pagerank, displayKind, name, location, nodeID)
				count++
			}
			if count == 0 {
				fmt.Fprintf(out, "no matches for %q\n", query)
			}
			return rows.Err()
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	cmd.Flags().IntVar(&limit, "limit", 25, "max matches to show")
	cmd.Flags().StringVar(&nodeType, "type", "", "filter by node type (file|symbol)")
	return cmd
}
