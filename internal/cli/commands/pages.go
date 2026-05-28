package commands

import (
	"database/sql"
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/generation/models"
	"github.com/repowise-dev/repowise-go/internal/persistence/wikistore"
)

// newPagesCmd is the read-only counterpart to `repowise generate`.
// "list" enumerates persisted pages; "show" prints one page's body.
func newPagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pages",
		Short: "Inspect wiki pages persisted by `repowise generate`",
		Long: `Read-only access to the wiki_pages table. Use ` + "`pages list`" + ` to
see what's been generated and ` + "`pages show <PATH>`" + ` to print the
markdown body of one page.`,
	}
	cmd.AddCommand(newPagesListCmd())
	cmd.AddCommand(newPagesShowCmd())
	return cmd
}

func newPagesListCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
		kind     string
		stale    bool
		limit    int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List persisted wiki pages",
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

			pages, err := wikistore.New(conn).ListByRepo(ctx, repoRow.ID)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  kind\ttarget\tv\tfresh\ttokens\tmodel\ttitle")

			count := 0
			for _, p := range pages {
				if kind != "" && string(p.PageType) != kind {
					continue
				}
				if stale && p.Freshness == models.FreshnessFresh {
					continue
				}
				if limit > 0 && count >= limit {
					break
				}
				tokens := fmt.Sprintf("%d/%d", p.InputTokens, p.OutputTokens)
				fmt.Fprintf(tw, "  %s\t%s\tv%d\t%s\t%s\t%s\t%s\n",
					p.PageType, p.TargetPath, p.Version, p.Freshness, tokens, p.ModelName, truncateColumn(p.Title, 60))
				count++
			}
			if count == 0 {
				fmt.Fprintln(out, "no pages match (run `repowise generate` first)")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	cmd.Flags().StringVar(&kind, "kind", "", "filter by page_type (file_overview|directory_overview|symbol_detail|architecture)")
	cmd.Flags().BoolVar(&stale, "stale", false, "show only stale pages")
	cmd.Flags().IntVar(&limit, "limit", 0, "max rows to show (0 = all)")
	return cmd
}

func newPagesShowCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
		kind     string
	)
	cmd := &cobra.Command{
		Use:   "show TARGET_PATH",
		Short: "Print the markdown body of one wiki page",
		Args:  cobra.ExactArgs(1),
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

			pageKind := models.PageKind(kind)
			if pageKind == "" {
				pageKind = models.PageKindFileOverview
			}
			page, err := wikistore.New(conn).GetByTarget(ctx, repoRow.ID, pageKind, args[0])
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("no %s page for %s (run `repowise generate --target %s`)", pageKind, args[0], args[0])
				}
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "# %s\n", page.Title)
			fmt.Fprintf(out, "_kind=%s  target=%s  version=%d  freshness=%s  model=%s_\n\n",
				page.PageType, page.TargetPath, page.Version, page.Freshness, page.ModelName)
			fmt.Fprintln(out, page.Content)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	cmd.Flags().StringVar(&kind, "kind", "", "page kind (default: file_overview)")
	return cmd
}

// truncateColumn clips long values for tabwriter output. A single tab
// stop is hard to read when content varies wildly.
func truncateColumn(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
