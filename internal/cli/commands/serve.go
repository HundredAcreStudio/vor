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
	"github.com/repowise-dev/repowise-go/internal/userconfig"
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
		autoMode        bool
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
				switch {
				case autoMode:
					// Span every workspace registered in the user-global
					// registry. The shared DB already holds all member
					// repos; the MCP layer resolves `repo` aliases across
					// all roots.
					reg, err := userconfig.LoadWorkspaces()
					if err != nil {
						return fmt.Errorf("load workspaces registry: %w", err)
					}
					if len(reg.Workspaces) == 0 {
						return fmt.Errorf("no workspaces registered — `repowise workspace register PATH`")
					}
					for _, w := range reg.Workspaces {
						mcpOpts.WorkspaceRoots = append(mcpOpts.WorkspaceRoots, w.Path)
					}
					// Default repo: the default alias of the first
					// registered workspace, so no-`repo` calls still work.
					if first, err := workspace.Load(reg.Workspaces[0].Path); err == nil && first.DefaultAlias != "" {
						if e, ok := first.Lookup(first.DefaultAlias); ok {
							if id, err := mcp.ResolveRepositoryID(ctx, conn, e.Path); err == nil {
								mcpOpts.RepositoryID = id
							}
						}
					}
					logger.Info("serve: auto mode", "workspaces", len(reg.Workspaces))
				case workspaceMode:
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
				default:
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

			// Record the daemon in ~/.local/state/repowise/daemon.json
			// so `repowise status` (or a future watch/auto-attach
			// flow) can detect it. Stale records after a hard kill are
			// caught by DaemonInfo.Alive() in the status path.
			daemonInfo := &userconfig.DaemonInfo{
				PID:         os.Getpid(),
				Addr:        bind,
				StartedAt:   time.Now().UTC(),
				DatabaseURL: cfg.DatabaseURL,
			}
			if workspaceMode {
				if root, err := filepath.Abs(repoPath); err == nil {
					daemonInfo.WorkspaceRoot = root
				}
			}
			if err := userconfig.SaveDaemon(daemonInfo); err != nil {
				logger.Warn("could not record daemon state", "err", err)
			}
			defer func() { _ = userconfig.ClearDaemon() }()

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
	cmd.Flags().BoolVar(&autoMode, "auto", false, "serve every workspace in the user-global registry (`repowise workspace register`)")
	return cmd
}
