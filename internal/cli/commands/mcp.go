package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/logging"
	"github.com/repowise-dev/repowise-go/internal/server/mcp"
)

// newMCPCmd starts a Model Context Protocol server on stdio. Run from
// Claude Code / Cursor as the configured MCP command.
func newMCPCmd() *cobra.Command {
	var repoPath string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server (stdio transport)",
		Long: `Starts a Model Context Protocol server speaking JSON-RPC over
stdin/stdout. The server exposes repowise tools (repowise_status,
repowise_hotspots, repowise_dead_code, repowise_health,
repowise_health_findings) backed by the persisted analysis database.

Typical usage from .mcp.json:

  {
    "servers": {
      "repowise": {
        "command": "repowise",
        "args": ["mcp", "--repo", "/path/to/repo"]
      }
    }
  }`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()

			abs, err := filepath.Abs(repoPath)
			if err != nil {
				return fmt.Errorf("resolve --repo: %w", err)
			}
			repoID, err := mcp.ResolveRepositoryID(ctx, conn, abs)
			if err != nil {
				return fmt.Errorf("resolve repo: %w", err)
			}

			// Logs go to stderr so they don't collide with the JSON-RPC
			// stream on stdout. Use JSON format unconditionally — humans
			// won't be reading this directly.
			logger := logging.New(logging.Options{
				Format: logging.FormatJSON,
				Level:  logging.ParseLevel("info"),
				Out:    os.Stderr,
			})

			srv, err := mcp.New(mcp.Options{
				DB:           conn,
				RepositoryID: repoID,
				Logger:       logger,
			})
			if err != nil {
				return err
			}
			return srv.ServeStdio()
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (used to resolve config + DB)")
	return cmd
}
