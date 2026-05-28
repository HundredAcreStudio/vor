package commands

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/logging"
	"github.com/HundredAcreStudio/vor/internal/persistence/pipelinestore"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/pipeline"
)

// newPipelineCmd is a group for pipeline-related subcommands. v1 ships
// 'log' (history), 'summary' (state tally), 'status' (latest-run
// verdict), and 'resume' (retry the latest failed run with the same
// run_id).
func newPipelineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Inspect pipeline_jobs history for a repository",
	}
	cmd.AddCommand(newPipelineLogCmd())
	cmd.AddCommand(newPipelineSummaryCmd())
	cmd.AddCommand(newPipelineStatusCmd())
	cmd.AddCommand(newPipelineResumeCmd())
	return cmd
}

func newPipelineLogCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
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
			repoRow, err := resolveReadRepo(ctx, conn, readRepoOptions{Path: repoPath, ID: repoID})
			if err != nil {
				return err
			}

			rows, err := pipelinestore.New(conn).LatestByRepo(ctx, repoRow.ID, limit)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no pipeline_jobs rows (run `vor init` first)")
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
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	cmd.Flags().IntVar(&limit, "limit", 50, "max rows to show")
	return cmd
}

// newPipelineStatusCmd shows the latest run's per-phase verdict so users
// can answer "is my index up to date?" without piecing it together from
// the log.
func newPipelineStatusCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the latest pipeline run's verdict",
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
			latest, err := pipelinestore.New(conn).LatestRun(ctx, repoRow.ID)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if latest == nil {
				fmt.Fprintln(out, "no pipeline run yet (run `vor init`)")
				return nil
			}
			fmt.Fprintf(out, "run_id:    %s\n", latest.RunID)
			fmt.Fprintf(out, "started:   %s\n", latest.StartedAt.Format("2006-01-02 15:04:05"))
			fmt.Fprintf(out, "updated:   %s\n", latest.UpdatedAt.Format("2006-01-02 15:04:05"))
			fmt.Fprintf(out, "overall:   %s\n\n", latest.Overall)

			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  phase\tstate\tduration\terror")
			// Group by phase, show the latest state per phase.
			seen := map[string]bool{}
			for i := len(latest.Phases) - 1; i >= 0; i-- {
				p := latest.Phases[i]
				if seen[p.Phase] {
					continue
				}
				seen[p.Phase] = true
				dur := p.UpdatedAt.Sub(p.StartedAt).Truncate(time.Millisecond)
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", p.Phase, p.State, dur, p.Error)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	return cmd
}

// newPipelineResumeCmd retries the latest failed run with the same
// run_id, so the audit trail shows attempt-chains rather than orphan
// runs. The store's persistence is idempotent (ReplaceAll semantics),
// so re-running is safe; this command is mostly an ergonomic wrapper.
func newPipelineResumeCmd() *cobra.Command {
	var (
		repoPath      string
		gitMaxCommits int
	)
	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Re-run the most recent failed pipeline run with the same run_id",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			absRoot := absRepoPath(repoPath)

			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, absRoot, "")
			if err != nil {
				return fmt.Errorf("ensure repo: %w", err)
			}

			logger := logging.New(logging.Options{
				Format: logging.FormatAuto,
				Level:  logging.ParseLevel("info"),
				Out:    os.Stderr,
			})
			res, err := pipeline.Run(ctx, pipeline.Options{
				RepoPath:      absRoot,
				RepositoryID:  repoRow.ID,
				DB:            conn,
				Mode:          pipeline.ModeResume,
				GitMaxCommits: gitMaxCommits,
				Logger:        logger,
			})
			if errors.Is(err, pipeline.ErrNothingToResume) {
				fmt.Fprintln(cmd.OutOrStdout(), "latest run already succeeded; nothing to resume")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nresumed run %s, %d phases re-ran\n",
				res.RunID, len(res.Phases))
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().IntVar(&gitMaxCommits, "git-max-commits", 0, "cap commits walked by the git phase (0 = default 10000)")
	return cmd
}

func newPipelineSummaryCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
	)
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
			repoRow, err := resolveReadRepo(ctx, conn, readRepoOptions{Path: repoPath, ID: repoID})
			if err != nil {
				return err
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
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	return cmd
}
