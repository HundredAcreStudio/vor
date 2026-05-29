package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/logging"
	"github.com/HundredAcreStudio/vor/internal/server/mcp"
)

// newMCPCmd starts a Model Context Protocol server on stdio. Run from
// Claude Code / Cursor as the configured MCP command. It serves a single
// repo: --repo resolves to one repository_id and every tool call queries
// it (the `repo` argument can still override per call).
func newMCPCmd() *cobra.Command {
	var repoPath string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server (stdio transport)",
		Long: `Starts a Model Context Protocol server speaking JSON-RPC over
stdin/stdout for a single repository:

  vor mcp --repo /path/to/repo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()

			logger := logging.New(logging.Options{
				Format: logging.FormatJSON,
				Level:  logging.ParseLevel("info"),
				Out:    os.Stderr,
			})

			abs, err := filepath.Abs(repoPath)
			if err != nil {
				return fmt.Errorf("resolve --repo: %w", err)
			}
			repoID, err := mcp.ResolveRepositoryID(ctx, conn, abs)
			if err != nil {
				return fmt.Errorf("resolve repo: %w", err)
			}

			// Optionally wire an LLM provider so the synthesis tools
			// (get_answer, get_why) work. Absent a key they degrade to
			// returning raw retrieved material — the server still starts.
			// Config is resolved from the DB (repo settings ← global ←
			// defaults); a failure degrades to defaults rather than aborting.
			boot := config.LoadBootstrap()
			cfg, _ := config.Resolve(ctx, conn, repoID, boot)
			provider, model := buildOptionalProvider(cfg)
			embedder, _ := buildEmbedder(cfg)

			opts := mcp.Options{
				DB: conn, Logger: logger, Provider: provider, Model: model,
				Embedder: embedder, RepositoryID: repoID,
			}

			srv, err := mcp.New(opts)
			if err != nil {
				return err
			}
			return srv.ServeStdio()
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path to serve")
	return cmd
}
