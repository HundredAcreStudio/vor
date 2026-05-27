package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/logging"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/pipeline"
)

// newUpdateCmd re-runs the indexing pipeline against a repo. Mirrors the
// Python `repowise update` shape: same effect as `init` but tagged
// Mode=ModeUpdate so observability can distinguish first-time index from
// re-runs. Pipeline persistence is idempotent (ReplaceAll semantics on
// every store), so this is safe to invoke repeatedly.
func newUpdateCmd() *cobra.Command {
	var (
		repoPath      string
		gitMaxCommits int
	)
	cmd := &cobra.Command{
		Use:   "update [PATH]",
		Short: "Re-index a repository (incremental — tagged 'update' for observability)",
		Long: `Walks every pipeline phase against the repo and replaces persisted
state. Equivalent to ` + "`repowise init`" + ` but Mode=update so the
pipeline_jobs row distinguishes a fresh index from a re-index.

For a force-rebuild that wipes prior state before re-indexing, use
` + "`repowise reindex`" + `.`,
		Args: cobra.MaximumNArgs(1),
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
				Mode:          pipeline.ModeUpdate,
				GitMaxCommits: gitMaxCommits,
				Logger:        logger,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nupdate complete (run %s, %d phases)\n",
				res.RunID, len(res.Phases))
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (overridden by positional PATH)")
	cmd.Flags().IntVar(&gitMaxCommits, "git-max-commits", 0, "cap commits walked by the git phase (0 = default 10000)")
	return cmd
}
