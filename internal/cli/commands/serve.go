package commands

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/logging"
	"github.com/HundredAcreStudio/vor/internal/server/autoindex"
	rhttp "github.com/HundredAcreStudio/vor/internal/server/http"
	"github.com/HundredAcreStudio/vor/internal/server/mcp"
	"github.com/HundredAcreStudio/vor/internal/userconfig"
	"github.com/HundredAcreStudio/vor/internal/workspace"
)

// newServeCmd starts the long-running HTTP daemon. By default it
// hosts both the /api/repos/{id}/* REST surface and the /mcp endpoint
// (MCP Streamable HTTP). Editor clients can attach to the same daemon
// instead of each spawning a stdio child via `vor mcp`.
//
// Single-repo mode (legacy): --repo resolves to one repository_id.
// HTTP routes use it as their default; MCP tools without an explicit
// `repo` argument fall back to it.
//
// Workspace mode: --workspace flips the daemon into multi-repo mode.
// --repo (or --workspace-root) becomes the workspace root; member
// repos are discoverable via the new vor_workspace_repos MCP
// tool and addressable per-call via the `repo` argument.
func newServeCmd() *cobra.Command {
	var (
		repoPath        string
		addr            string
		mcpEnabled      bool
		workspaceMode   bool
		workspaceRootIn string
		autoMode        bool
		watchEnabled    bool
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API + MCP daemon",
		Long: `Starts a long-running HTTP server exposing vor's analysis
output. Two surfaces live on one port:

  /api/repos/{id}/*  REST endpoints — graph, hotspots, dead code,
                     health, decisions, externals, pages, pipeline,
                     workspace co-changes.
  /mcp               MCP over Streamable HTTP — editor clients
                     connect here instead of running a stdio child.

Configuration comes from VOR_DB_URL / .vor/config.yaml.
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
				handler, err := buildMCPHandler(ctx, conn, cfg, logger, repoPath, workspaceRootIn, autoMode, workspaceMode)
				if err != nil {
					return err
				}
				httpOpts.MCPHandler = handler
			}

			srv, err := rhttp.New(httpOpts)
			if err != nil {
				return err
			}

			// Record the daemon in ~/.local/state/vor/daemon.json
			// so `vor status` (or a future watch/auto-attach
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

			if mcpEnabled {
				printMCPInstructions(cmd.OutOrStdout(), bind)
			}

			// Keep the index fresh for the daemon's lifetime: one incremental
			// reindex per repo at startup (so a daemon launched before its
			// first ingest finished doesn't serve an empty graph), then watch
			// each repo's source tree. Tied to ctx so shutdown cancels it.
			if watchEnabled {
				w := autoindex.New(autoindex.Options{DB: conn, Logger: logger})
				go func() {
					if err := w.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
						logger.Error("auto-reindex watcher stopped", "err", err)
					}
				}()
			}

			if err := srv.ListenAndServe(ctx); err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (or workspace root with --workspace)")
	cmd.Flags().StringVar(&addr, "addr", "", "host:port to bind (overrides VOR_HOST/PORT)")
	cmd.Flags().BoolVar(&mcpEnabled, "mcp", true, "mount the MCP server at /mcp (default true)")
	cmd.Flags().BoolVar(&workspaceMode, "workspace", false, "serve every repo registered in the workspace")
	cmd.Flags().StringVar(&workspaceRootIn, "workspace-root", "", "workspace root (defaults to --repo when --workspace is set)")
	cmd.Flags().BoolVar(&autoMode, "auto", false, "serve every workspace in the user-global registry (`vor workspace register`)")
	cmd.Flags().BoolVar(&watchEnabled, "watch", true, "auto-reindex on startup and on source changes (default true)")
	return cmd
}

// buildMCPHandler wires an MCP server (LLM synthesis when configured) and
// returns its Streamable-HTTP handler for mounting at /mcp.
func buildMCPHandler(ctx context.Context, conn *sql.DB, cfg config.Config, logger *slog.Logger, repoPath, workspaceRootIn string, autoMode, workspaceMode bool) (http.Handler, error) {
	provider, model := buildOptionalProvider(cfg)
	embedder, _ := buildEmbedder(cfg)
	mcpOpts := mcp.Options{DB: conn, Logger: logger, Provider: provider, Model: model, Embedder: embedder}
	if provider != nil {
		logger.Info("serve: LLM synthesis enabled", "provider", cfg.Provider)
	}
	if err := configureMCPScope(ctx, conn, logger, &mcpOpts, repoPath, workspaceRootIn, autoMode, workspaceMode); err != nil {
		return nil, err
	}
	mcpSrv, err := mcp.New(mcpOpts)
	if err != nil {
		return nil, fmt.Errorf("init MCP: %w", err)
	}
	logger.Info("serve: MCP mounted at /mcp")
	return mcpSrv.HTTPHandler(), nil
}

// configureMCPScope sets the repo/workspace targeting on mcpOpts for the
// selected serve mode: --auto (every registered workspace), --workspace
// (one workspace), or single-repo (default).
func configureMCPScope(ctx context.Context, conn *sql.DB, logger *slog.Logger, mcpOpts *mcp.Options, repoPath, workspaceRootIn string, autoMode, workspaceMode bool) error {
	switch {
	case autoMode:
		reg, err := userconfig.LoadWorkspaces()
		if err != nil {
			return fmt.Errorf("load workspaces registry: %w", err)
		}
		if len(reg.Workspaces) == 0 {
			return fmt.Errorf("no workspaces registered — `vor workspace register PATH`")
		}
		for _, w := range reg.Workspaces {
			mcpOpts.WorkspaceRoots = append(mcpOpts.WorkspaceRoots, w.Path)
		}
		// Default repo: the default alias of the first registered workspace,
		// so no-`repo` calls still work.
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
			return fmt.Errorf("no repos registered at %s — add some with `vor workspace add`", abs)
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
	return nil
}

// printMCPInstructions writes copy-paste instructions for attaching an MCP
// client to the running daemon. bind is the listen address (e.g. ":7337"
// or "0.0.0.0:7337"); we render a reachable localhost URL from it.
func printMCPInstructions(out io.Writer, bind string) {
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		host, port = "", strings.TrimPrefix(bind, ":")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	url := fmt.Sprintf("http://%s/mcp", net.JoinHostPort(host, port))

	fmt.Fprintf(out, `
MCP server ready at %s

Connect an AI coding agent to this daemon:

  Claude Code:
    claude mcp add --transport http vor %s

  Cursor / generic MCP clients — add to the client's mcp.json:
    {
      "mcpServers": {
        "vor": { "transport": "http", "url": "%s" }
      }
    }

The daemon serves every indexed repo in its database; MCP tools accept a
`+"`repo`"+` argument to target one (see vor_workspace_repos).

`, url, url, url)
}
