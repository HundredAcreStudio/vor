package commands

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

// newDeleteCmd removes a repository's row and (via ON DELETE CASCADE)
// every persisted analysis table associated with it. Files on disk are
// not touched.
//
// The on-disk source tree, git history, and config file
// (.repowise/config.yaml) survive — only the DB-side index is dropped.
func newDeleteCmd() *cobra.Command {
	var (
		repoPath string
		confirm  bool
	)
	cmd := &cobra.Command{
		Use:   "delete [PATH]",
		Short: "Drop a repository's persisted index (files on disk untouched)",
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
					"Would delete every persisted row for %s (repo id %s).\n"+
						"Re-run with --yes to proceed.\n", absRoot, existing.ID)
				return nil
			}
			if err := store.Delete(ctx, existing.ID); err != nil {
				return fmt.Errorf("delete: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted repo %s (%s)\n", existing.ID, absRoot)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (overridden by positional PATH)")
	cmd.Flags().BoolVar(&confirm, "yes", false, "confirm the destructive delete")
	return cmd
}
