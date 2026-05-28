package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/config"
	"github.com/repowise-dev/repowise-go/internal/logging"
	rhttp "github.com/repowise-dev/repowise-go/internal/server/http"
	"github.com/repowise-dev/repowise-go/internal/server/mcp"
	"github.com/repowise-dev/repowise-go/internal/workspace"
)

// newServeCmd starts the long-running HTTP daemon. By default it
// hosts both the /api/repos/{id}/* REST surface and the /mcp endpoint
// (MCP Streamable HTTP). Editor clients can attach to the same daemon
// instead of each spawning a stdio child via `repowise mcp`.
//
// Single-repo mode (legacy): --repo resolves to one repository_id.
// HTTP routes use it as their default; MCP tools without an explicit
// `repo` argument fall back to it.
//
// Workspace mode: --workspace flips the daemon into multi-repo mode.
// --repo (or --workspace-root) becomes the workspace root; member
// repos are discoverable via the new repowise_workspace_repos MCP
// tool and addressable per-call via the `repo` argument.
func newServeCmd() *cobra.Command {
	var (
		repoPath        string
		addr            string
		mcpEnabled      bool
		workspaceMode   bool
		workspaceRootIn string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API + MCP daemon",
		Long: `Starts a long-running HTTP server exposing repowise's analysis
output. Two surfaces live on one port:

  /api/repos/{id}/*  REST endpoints — graph, hotspots, dead code,
                     health, decisions, externals, pages, pipeline,
                     workspace co-changes.
  /mcp               MCP over Streamable HTTP — editor clients
                     connect here instead of running a stdio child.

Configuration comes from REPOWISE_DB_URL / .repowise/config.yaml.
Use --workspace to serve every repo in the workspace from one
daemon (one DB holds N repos; MCP tools route per-call by the
'repo' argument).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(repoPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()

			logger := logging.New(logging.Options{
				Level:  logging.ParseLevel(cfg.LogLevel),
				Format: logging.FormatAuto,
				Out:    os.Stderr,
			})

			bind := addr
			if bind == "" {
				bind = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
			}

			// Optional MCP — defaults on. The HTTP server mounts the
			// handler at /mcp when present; passing nil disables.
			httpOpts := rhttp.Options{
				DB:           conn,
				Logger:       logger,
				Addr:         bind,
				ReadTimeout:  10 * time.Second,
				WriteTimeout: 30 * time.Second,
				IdleTimeout:  60 * time.Second,
			}

			if mcpEnabled {
				mcpOpts := mcp.Options{DB: conn, Logger: logger}
				if workspaceMode {
					root := workspaceRootIn
					if root == "" {
						root = repoPath
					}
					abs, err := filepath.Abs(root)
					if err != nil {
						return fmt.Errorf("resolve workspace root: %w", err)
					}
					state, err := workspace.Load(abs)
					if err != nil {
						return fmt.Errorf("load workspace.json: %w", err)
					}
					if len(state.Repos) == 0 {
						return fmt.Errorf("no repos registered at %s — add some with `repowise workspace add`", abs)
					}
					mcpOpts.WorkspaceRoot = abs
					if state.DefaultAlias != "" {
						if e, ok := state.Lookup(state.DefaultAlias); ok {
							if id, err := mcp.ResolveRepositoryID(ctx, conn, e.Path); err == nil {
								mcpOpts.RepositoryID = id
							}
						}
					}
					logger.Info("serve: workspace mode", "root", abs, "repos", len(state.Repos))
				} else {
					abs, err := filepath.Abs(repoPath)
					if err != nil {
						return fmt.Errorf("resolve --repo: %w", err)
					}
					repoID, err := mcp.ResolveRepositoryID(ctx, conn, abs)
					if err != nil {
						return fmt.Errorf("resolve repo: %w", err)
					}
					mcpOpts.RepositoryID = repoID
				}
				mcpSrv, err := mcp.New(mcpOpts)
				if err != nil {
					return fmt.Errorf("init MCP: %w", err)
				}
				httpOpts.MCPHandler = mcpSrv.HTTPHandler()
				logger.Info("serve: MCP mounted at /mcp")
			}

			srv, err := rhttp.New(httpOpts)
			if err != nil {
				return err
			}
			if err := srv.ListenAndServe(ctx); err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (or workspace root with --workspace)")
	cmd.Flags().StringVar(&addr, "addr", "", "host:port to bind (overrides REPOWISE_HOST/PORT)")
	cmd.Flags().BoolVar(&mcpEnabled, "mcp", true, "mount the MCP server at /mcp (default true)")
	cmd.Flags().BoolVar(&workspaceMode, "workspace", false, "serve every repo registered in the workspace")
	cmd.Flags().StringVar(&workspaceRootIn, "workspace-root", "", "workspace root (defaults to --repo when --workspace is set)")
	return cmd
}
