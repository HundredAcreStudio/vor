package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/logging"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/pipeline"
	"github.com/HundredAcreStudio/vor/internal/pipeline/tasks"
)

// newReindexCmd re-runs the indexing pipeline for a repository. By default
// it is NON-DESTRUCTIVE: an incremental update that re-parses changed files
// and recomputes derived state in place, preserving the generated wiki and
// health history (so it never triggers a costly full LLM re-gen). Pass --hard
// for the old "scorched earth" rebuild — drop the repository row (cascading
// deletes wipe every persisted table) and rebuild from scratch — for the rare
// case where persisted state is stale in a way an update can't fix (schema
// migration, corruption).
func newReindexCmd() *cobra.Command {
	var (
		repoPath      string
		gitMaxCommits int
		confirm       bool
		hard          bool
	)
	cmd := &cobra.Command{
		Use:   "reindex [PATH]",
		Short: "Re-run the indexing pipeline (non-destructive; --hard wipes and rebuilds)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root := repoPath
			if len(args) > 0 {
				root = args[0]
			}
			absRoot, err := filepath.Abs(root)
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}

			conn, _, err := openDB(ctx, root)
			if err != nil {
				return err
			}
			defer conn.Close()
			store := repos.New(conn)
			existing, err := store.EnsureByLocalPath(ctx, absRoot, "")
			if err != nil {
				return fmt.Errorf("locate repo: %w", err)
			}

			logger := logging.New(logging.Options{
				Format: logging.FormatAuto,
				Level:  logging.ParseLevel("info"),
				Out:    os.Stderr,
			})

			// Default path: non-destructive incremental update. Preserves the
			// wiki + health and only re-does work for changed files.
			if !hard {
				res, err := pipeline.Run(ctx, pipeline.Options{
					RepoPath:      absRoot,
					RepositoryID:  existing.ID,
					DB:            conn,
					Mode:          pipeline.ModeUpdate,
					GitMaxCommits: gitMaxCommits,
					Logger:        logger,
				})
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "\nreindex complete (run %s, %d phases)\n",
					res.RunID, len(res.Phases))
				reportTaskOutcomes(cmd, tasks.AfterPipeline(ctx, conn, existing.ID, absRoot, logger, tasks.PipelineOutcome{
					Incremental: true,
					Changed:     res.ChangedFiles,
				}))
				return nil
			}

			// --hard: scorched earth. Gated behind --yes because it wipes the
			// wiki + all derived state and forces a full (credit-spending) re-gen.
			if !confirm {
				fmt.Fprintf(cmd.OutOrStdout(),
					"--hard will DELETE all persisted state for %s (repo id %s), including the\n"+
						"generated wiki, and rebuild from scratch (a full LLM wiki re-gen costs credits).\n"+
						"Re-run with --hard --yes to proceed, or drop --hard for a non-destructive update.\n",
					absRoot, existing.ID)
				return nil
			}
			if err := store.Delete(ctx, existing.ID); err != nil {
				return fmt.Errorf("delete repo row: %w", err)
			}
			// Recreate with the SAME id (and tracked/ephemeral state) so the
			// rebuild preserves the repo's identity. Repo-scoped settings
			// (e.g. health_rules) key on the id and have no FK to repositories,
			// so a fresh id would silently orphan them — and a tracked repo
			// would lose its watch.
			fresh, err := store.Reinsert(ctx, existing)
			if err != nil {
				return fmt.Errorf("recreate repo row: %w", err)
			}
			res, err := pipeline.Run(ctx, pipeline.Options{
				RepoPath:      absRoot,
				RepositoryID:  fresh.ID,
				DB:            conn,
				Mode:          pipeline.ModeInit,
				GitMaxCommits: gitMaxCommits,
				Logger:        logger,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nhard reindex complete (new run %s, %d phases)\n",
				res.RunID, len(res.Phases))
			reportTaskOutcomes(cmd, tasks.AfterPipeline(ctx, conn, fresh.ID, absRoot, logger, tasks.PipelineOutcome{}))
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (overridden by positional PATH)")
	cmd.Flags().IntVar(&gitMaxCommits, "git-max-commits", 0, "cap commits walked by the git phase")
	cmd.Flags().BoolVar(&hard, "hard", false, "destructive: wipe all persisted state and rebuild from scratch")
	cmd.Flags().BoolVar(&confirm, "yes", false, "confirm the destructive --hard rebuild")
	return cmd
}
