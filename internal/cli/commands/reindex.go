package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/logging"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/pipeline"
)

// newReindexCmd is the "scorched earth" rebuild: drop the repository row
// (cascading deletes wipe every persisted table for it) then re-run the
// full pipeline. Use when you suspect persisted state is stale in a way
// idempotent re-indexing won't fix (e.g. schema migration, corruption).
//
// For the lighter version that just re-runs the pipeline without wiping
// first, use ` + "`vor update`" + `.
func newReindexCmd() *cobra.Command {
	var (
		repoPath      string
		gitMaxCommits int
		confirm       bool
	)
	cmd := &cobra.Command{
		Use:   "reindex [PATH]",
		Short: "Wipe a repository's persisted state and re-run the full pipeline",
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

			if !confirm {
				fmt.Fprintf(cmd.OutOrStdout(),
					"This will delete all persisted state for %s (repo id %s) and re-index.\n"+
						"Re-run with --yes to proceed.\n", absRoot, existing.ID)
				return nil
			}

			if err := store.Delete(ctx, existing.ID); err != nil {
				return fmt.Errorf("delete repo row: %w", err)
			}
			fresh, err := store.EnsureByLocalPath(ctx, absRoot, "")
			if err != nil {
				return fmt.Errorf("recreate repo row: %w", err)
			}

			logger := logging.New(logging.Options{
				Format: logging.FormatAuto,
				Level:  logging.ParseLevel("info"),
				Out:    os.Stderr,
			})
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
			fmt.Fprintf(cmd.OutOrStdout(), "\nreindex complete (new run %s, %d phases)\n",
				res.RunID, len(res.Phases))
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (overridden by positional PATH)")
	cmd.Flags().IntVar(&gitMaxCommits, "git-max-commits", 0, "cap commits walked by the git phase")
	cmd.Flags().BoolVar(&confirm, "yes", false, "confirm the destructive delete")
	return cmd
}
