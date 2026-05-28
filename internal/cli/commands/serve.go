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
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/server/autoindex"
	rhttp "github.com/HundredAcreStudio/vor/internal/server/http"
	"github.com/HundredAcreStudio/vor/internal/server/mcp"
	"github.com/HundredAcreStudio/vor/internal/server/registry"
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
'repo' argument).

With a 'repos:' list in ~/.config/vor/config.yaml and no explicit
--repo/--workspace/--auto, a bare 'vor serve' tracks that whole set
from one shared database (the state-dir DB unless VOR_DB_URL pins one),
indexing and watching each. Address them per-call by local path or
repository id.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(repoPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			logger := logging.New(logging.Options{
				Level:  logging.ParseLevel(cfg.LogLevel),
				Format: logging.FormatAuto,
				Out:    os.Stderr,
			})

			// repos-list mode: when the user hasn't pinned a single --repo
			// (or a workspace) and the config carries a `repos:` list, this
			// daemon tracks that whole set from one shared DB — the same
			// "N repos, one database" model as --workspace/--auto.
			reposMode := !workspaceMode && !autoMode &&
				!cmd.Flags().Changed("repo") && len(cfg.Repos) > 0

			// In repos-list mode the DB can't default to a single repo's
			// .vor/wiki.db — fall back to a machine-wide DB in the state dir
			// (unless VOR_DB_URL pins one explicitly).
			dbURL := cfg.DatabaseURL
			if reposMode && dbURL == "" {
				dir, derr := userconfig.StateDir()
				if derr != nil {
					return fmt.Errorf("resolve state dir for shared db: %w", derr)
				}
				dbURL = "sqlite:" + filepath.Join(dir, "wiki.db")
			}

			var (
				conn    *sql.DB
				dialect db.Dialect
			)
			if dbURL != "" {
				conn, dialect, err = db.Open(ctx, db.OpenOptions{URL: dbURL})
			} else {
				conn, dialect, err = openDB(ctx, repoPath)
			}
			if err != nil {
				return err
			}
			defer conn.Close()
			// Idempotent — also lets a fresh shared DB come up without a
			// prior `vor init`.
			if err := migrations.Up(ctx, conn, dialect); err != nil {
				return fmt.Errorf("apply migrations: %w", err)
			}

			var reposDefaultID string
			if reposMode {
				ids, rerr := registerGlobalRepos(ctx, conn, cfg.Repos, logger)
				if rerr != nil {
					return rerr
				}
				if len(ids) == 0 {
					return fmt.Errorf("repos: configured list resolved to no usable repositories")
				}
				reposDefaultID = ids[0]
				logger.Info("serve: tracking repos from config", "count", len(ids))
			}

			// Auto-reindex watcher + registrar. The watcher watches the DB's
			// tracked repos and supports runtime register/unregister; the
			// registrar is the shared service the REST + MCP endpoints call.
			// Enablement: config/env default, with an explicit --watch winning.
			watchOn := watchEnabled
			if !cmd.Flags().Changed("watch") && cfg.Watch.Enabled != nil {
				watchOn = *cfg.Watch.Enabled
			}
			var (
				watcher *autoindex.Watcher
				reg     *registry.Registrar
			)
			if watchOn {
				var debounce time.Duration
				if cfg.Watch.Debounce != "" {
					if d, perr := time.ParseDuration(cfg.Watch.Debounce); perr == nil {
						debounce = d
					} else {
						logger.Warn("invalid watch.debounce, using default", "value", cfg.Watch.Debounce, "err", perr)
					}
				}
				watcher = autoindex.New(autoindex.Options{DB: conn, Logger: logger, Debounce: debounce})
				reg = registry.New(conn, watcher, logger)
			}

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
				Registrar:    reg,
			}

			if mcpEnabled {
				handler, err := buildMCPHandler(ctx, conn, cfg, logger, repoPath, workspaceRootIn, autoMode, workspaceMode, reposDefaultID, reg)
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

			// Run the auto-reindex watcher for the daemon's lifetime. It
			// watches the DB's tracked repos and is driven at runtime by the
			// register/unregister endpoints through the registrar. Tied to ctx
			// so shutdown cancels any in-flight reindex cleanly.
			if watcher != nil {
				go func() {
					if err := watcher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
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
	cmd.Flags().BoolVar(&watchEnabled, "watch", true, "auto-reindex on startup and on source changes; overrides config `watch.enabled` (default true)")
	return cmd
}

// buildMCPHandler wires an MCP server (LLM synthesis when configured) and
// returns its Streamable-HTTP handler for mounting at /mcp.
func buildMCPHandler(ctx context.Context, conn *sql.DB, cfg config.Config, logger *slog.Logger, repoPath, workspaceRootIn string, autoMode, workspaceMode bool, reposDefaultID string, reg *registry.Registrar) (http.Handler, error) {
	provider, model := buildOptionalProvider(cfg)
	embedder, _ := buildEmbedder(cfg)
	mcpOpts := mcp.Options{DB: conn, Logger: logger, Provider: provider, Model: model, Embedder: embedder, Registrar: reg}
	if provider != nil {
		logger.Info("serve: LLM synthesis enabled", "provider", cfg.Provider)
	}
	if err := configureMCPScope(ctx, conn, logger, &mcpOpts, repoPath, workspaceRootIn, autoMode, workspaceMode, reposDefaultID); err != nil {
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
func configureMCPScope(ctx context.Context, conn *sql.DB, logger *slog.Logger, mcpOpts *mcp.Options, repoPath, workspaceRootIn string, autoMode, workspaceMode bool, reposDefaultID string) error {
	switch {
	case reposDefaultID != "":
		// repos-list mode: repos are already registered in the DB; default
		// no-`repo` calls to the first, and let clients address the rest by
		// local path or repository id (see resolveRepoSpec).
		mcpOpts.RepositoryID = reposDefaultID
		logger.Info("serve: repos-list mode", "default_repo", reposDefaultID)
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

// registerGlobalRepos ensures every path in the config `repos:` list has a
// row in the shared DB so the watcher (repos.List) and MCP scope can see
// them. Paths support ~; non-directories and duplicates are skipped with a
// warning. Returns the resolved repository ids in input order.
func registerGlobalRepos(ctx context.Context, conn *sql.DB, paths []string, logger *slog.Logger) ([]string, error) {
	store := repos.New(conn)
	seen := map[string]bool{}
	var ids []string
	for _, raw := range paths {
		abs, err := filepath.Abs(expandUser(raw))
		if err != nil {
			logger.Warn("repos: cannot resolve path, skipping", "path", raw, "err", err)
			continue
		}
		if seen[abs] {
			continue
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			logger.Warn("repos: not a directory, skipping", "path", abs)
			continue
		}
		r, err := store.EnsureByLocalPath(ctx, abs, "")
		if err != nil {
			logger.Warn("repos: could not register, skipping", "path", abs, "err", err)
			continue
		}
		if err := store.SetTracked(ctx, r.ID, true, false); err != nil {
			logger.Warn("repos: could not mark tracked, skipping", "path", abs, "err", err)
			continue
		}
		seen[abs] = true
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// expandUser expands a leading ~ (or ~/) to the user's home directory.
// Other forms are returned unchanged.
func expandUser(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
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
