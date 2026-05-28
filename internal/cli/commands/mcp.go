package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/logging"
	"github.com/HundredAcreStudio/vor/internal/server/mcp"
	"github.com/HundredAcreStudio/vor/internal/workspace"
)

// newMCPCmd starts a Model Context Protocol server on stdio. Run from
// Claude Code / Cursor as the configured MCP command.
//
// Two operating modes:
//   - default — single repo. Resolves --repo to one repository_id;
//     every tool call queries that repo.
//   - --workspace — the daemon serves N repos from one shared DB.
//     Tool calls pass a `repo` argument (alias / id / path). A new
//     `vor_workspace_repos` tool lists the registered repos so
//     agents can discover them.
//
// In --workspace mode, --repo is treated as the workspace root
// (where .vor/workspace.json lives), not a single repo path.
func newMCPCmd() *cobra.Command {
	var (
		repoPath        string
		workspaceMode   bool
		workspaceRootIn string
	)
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server (stdio transport)",
		Long: `Starts a Model Context Protocol server speaking JSON-RPC over
stdin/stdout.

Single-repo mode (default):
  vor mcp --repo /path/to/repo

Workspace mode — one daemon serving every member repo:
  vor mcp --workspace [--workspace-root /path/to/workspace]
  # tool calls pass a 'repo' argument (alias from workspace.json)`,
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

			// Optionally wire an LLM provider so the synthesis tools
			// (get_answer, get_why) work. Absent a key they degrade to
			// returning raw retrieved material — the server still starts.
			cfg, _ := config.Load(repoPath)
			provider, model := buildOptionalProvider(cfg)
			embedder, _ := buildEmbedder(cfg)

			opts := mcp.Options{DB: conn, Logger: logger, Provider: provider, Model: model, Embedder: embedder}

			if workspaceMode {
				wsRoot := workspaceRootIn
				if wsRoot == "" {
					wsRoot = repoPath
				}
				abs, err := filepath.Abs(wsRoot)
				if err != nil {
					return fmt.Errorf("resolve workspace root: %w", err)
				}
				// Verify workspace.json exists at the resolved root.
				state, err := workspace.Load(abs)
				if err != nil {
					return fmt.Errorf("load workspace.json: %w", err)
				}
				if len(state.Repos) == 0 {
					return fmt.Errorf(
						"no repos registered at %s — add some with `vor workspace add`", abs)
				}
				opts.WorkspaceRoot = abs
				// Optional default: the workspace's default alias resolves
				// to a concrete repo id so legacy callers (no `repo`
				// argument) still get a sensible answer.
				if state.DefaultAlias != "" {
					if e, ok := state.Lookup(state.DefaultAlias); ok {
						if id, err := mcp.ResolveRepositoryID(ctx, conn, e.Path); err == nil {
							opts.RepositoryID = id
						}
					}
				}
			} else {
				abs, err := filepath.Abs(repoPath)
				if err != nil {
					return fmt.Errorf("resolve --repo: %w", err)
				}
				repoID, err := mcp.ResolveRepositoryID(ctx, conn, abs)
				if err != nil {
					return fmt.Errorf("resolve repo: %w", err)
				}
				opts.RepositoryID = repoID
			}

			srv, err := mcp.New(opts)
			if err != nil {
				return err
			}
			return srv.ServeStdio()
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (single-repo mode) or workspace root (with --workspace)")
	cmd.Flags().BoolVar(&workspaceMode, "workspace", false, "serve every repo registered in the workspace from one daemon")
	cmd.Flags().StringVar(&workspaceRootIn, "workspace-root", "", "workspace root (defaults to --repo when --workspace is set)")
	return cmd
}
