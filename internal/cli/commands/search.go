package commands

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/config"
	"github.com/repowise-dev/repowise-go/internal/persistence/vector"
)

// newSearchCmd searches the persisted graph_nodes by name (substring
// match). With --semantic it instead ranks wiki pages by embedding
// similarity (requires `repowise embed`).
func newSearchCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
		limit    int
		nodeType string
		semantic bool
	)
	cmd := &cobra.Command{
		Use:   "search [QUERY]",
		Short: "Search persisted symbols by name (or wiki pages with --semantic)",
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

			if semantic {
				return runSemanticSearch(ctx, cmd, conn, repoPath, repoRow.ID, query, limit)
			}

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
					startLine                           int
					pagerank                            float64
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
	cmd.Flags().BoolVar(&semantic, "semantic", false, "rank wiki pages by embedding similarity (requires `repowise embed`)")
	return cmd
}

// runSemanticSearch embeds the query and ranks wiki pages by cosine
// similarity. Errors clearly when no embeddings exist so the user knows
// to run `repowise embed` first.
func runSemanticSearch(ctx context.Context, cmd *cobra.Command, conn *sql.DB, repoPath, repoID, query string, limit int) error {
	cfg, err := config.Load(repoPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	embedder, err := buildEmbedder(cfg)
	if err != nil {
		return fmt.Errorf("build embedder: %w", err)
	}
	vstore := vector.New(conn)
	n, err := vstore.Count(ctx, repoID)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no embeddings for this repo — run `repowise embed` first")
	}
	vecs, err := embedder.Embed(ctx, []string{query})
	if err != nil {
		return fmt.Errorf("embed query: %w", err)
	}
	matches, err := vstore.Search(ctx, repoID, vecs[0], vector.KindPage, limit)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(matches) == 0 {
		fmt.Fprintf(out, "no semantic matches for %q\n", query)
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "  score\ttitle\tpage")
	for _, m := range matches {
		var title string
		_ = conn.QueryRowContext(ctx,
			`SELECT COALESCE(title,'') FROM wiki_pages WHERE repository_id = ? AND target_path = ?`,
			repoID, m.TargetPath).Scan(&title)
		fmt.Fprintf(tw, "  %.4f\t%s\t%s\n", m.Score, title, m.TargetPath)
	}
	return nil
}
