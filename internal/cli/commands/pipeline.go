package commands

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/persistence/pipelinestore"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

// newPipelineCmd is a group for pipeline-related subcommands. v1 ships
// 'log' (history) and 'summary' (state tally). Future passes can add
// 'resume' once checkpointing lands.
func newPipelineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Inspect pipeline_jobs history for a repository",
	}
	cmd.AddCommand(newPipelineLogCmd())
	cmd.AddCommand(newPipelineSummaryCmd())
	return cmd
}

func newPipelineLogCmd() *cobra.Command {
	var (
		repoPath string
		limit    int
	)
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show recent pipeline phase executions, newest first",
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

			rows, err := pipelinestore.New(conn).LatestByRepo(ctx, repoRow.ID, limit)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no pipeline_jobs rows (run `repowise init` first)")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  started_at\tphase\tstate\terror")
			for _, r := range rows {
				errCol := ""
				if r.State == pipelinestore.StateFailed {
					errCol = r.Error
				}
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n",
					r.StartedAt.Format("2006-01-02 15:04:05"), r.Phase, r.State, errCol)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().IntVar(&limit, "limit", 50, "max rows to show")
	return cmd
}

func newPipelineSummaryCmd() *cobra.Command {
	var repoPath string
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Show pipeline_jobs counts per state",
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
			counts, err := pipelinestore.New(conn).CountByState(ctx, repoRow.ID)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			defer tw.Flush()
			for _, state := range []string{
				pipelinestore.StatePending,
				pipelinestore.StateRunning,
				pipelinestore.StateCompleted,
				pipelinestore.StateFailed,
			} {
				fmt.Fprintf(tw, "  %s\t%d\n", state, counts[state])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	return cmd
}
