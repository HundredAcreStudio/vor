// Package mcp exposes repowise's analysis output via the Model Context
// Protocol. Tools are stdio-driven so editor integrations (Claude Code /
// Cursor) can read graph, git, dead-code, and health data without HTTP.
//
// The server can be configured in two modes:
//
//  1. Single-repo (legacy). Options.RepositoryID is set; every tool
//     call queries that repo unless the call explicitly overrides via
//     the `repo` argument.
//
//  2. Multi-repo / workspace. Options.WorkspaceRoot points at a
//     directory holding .repowise/workspace.json. Each tool call
//     MUST supply a `repo` argument (alias, full id, or local path).
//     A repowise_workspace_repos tool lets agents discover the
//     registered repos.
//
// The two modes can coexist: if both RepositoryID and WorkspaceRoot
// are set, RepositoryID is the default and the workspace data is
// available for alias lookups.
package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/providers"
	"github.com/repowise-dev/repowise-go/internal/version"
	"github.com/repowise-dev/repowise-go/internal/workspace"
)

// Options configures Server.
type Options struct {
	// DB is the open *sql.DB read by tool handlers.
	DB *sql.DB

	// RepositoryID is the default repo for tool calls that don't
	// override via a `repo` argument. Required when WorkspaceRoot is
	// empty; optional otherwise.
	RepositoryID string

	// WorkspaceRoot enables multi-repo mode when non-empty. The path
	// holds .repowise/workspace.json; tool calls can pass `repo` as
	// an alias from that workspace. Convenience for the single-
	// workspace case — appended to WorkspaceRoots at construction.
	WorkspaceRoot string

	// WorkspaceRoots is the multi-workspace form, used by
	// `repowise serve --auto` to span every registered workspace.
	// Alias resolution searches each root in order; the first match
	// wins. repowise_workspace_repos unions members across all roots.
	WorkspaceRoots []string

	// Provider powers the LLM-synthesis tools (get_answer, get_why).
	// Optional — when nil those tools degrade to returning the
	// retrieved raw context with a "synthesis unavailable" note rather
	// than erroring. get_context never needs it (pure data assembly).
	Provider providers.Provider

	// Model is passed through on synthesis requests. Empty lets the
	// provider pick its default.
	Model string

	// Embedder powers semantic search in repowise_search. Optional —
	// when nil (or when no embeddings exist for the repo) the tool
	// falls back to the LIKE path over graph_nodes. Must match the
	// embedder used by `repowise embed` for results to be meaningful.
	Embedder providers.Embedder

	// Logger receives structured tool-call logs. Defaults to slog.Default().
	Logger *slog.Logger
}

// workspaceRoots returns the effective list of workspace roots: the
// plural slice plus the singular convenience field, de-duplicated.
func (o Options) workspaceRoots() []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(o.WorkspaceRoot)
	for _, r := range o.WorkspaceRoots {
		add(r)
	}
	return out
}

// Server wraps mark3labs/mcp-go's MCPServer with repowise's tool surface.
type Server struct {
	opts Options
	srv  *server.MCPServer
}

// New constructs a Server with the given options. Tool registrations
// happen during construction so the returned server is ready to serve.
func New(opts Options) (*Server, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("Options.DB is required")
	}
	if opts.RepositoryID == "" && len(opts.workspaceRoots()) == 0 {
		return nil, fmt.Errorf("Options.RepositoryID, WorkspaceRoot, or WorkspaceRoots is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	mcpSrv := server.NewMCPServer(
		"repowise",
		version.Current().Version,
	)
	s := &Server{opts: opts, srv: mcpSrv}
	s.registerTools()
	return s, nil
}

// ServeStdio runs the MCP loop over stdin/stdout. Returns when the client
// disconnects or ctx is cancelled (graceful shutdown).
func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.srv)
}

// MCPServer returns the underlying mcp-go server. Exposed so tests can
// drive it directly without stdio.
func (s *Server) MCPServer() *server.MCPServer { return s.srv }

// HTTPHandler wraps the MCP server in an http.Handler implementing the
// MCP Streamable HTTP transport. Mount it under a path on any HTTP
// router (chi, stdlib mux, ...) to serve MCP over the network — the
// same transport works for short-lived requests (POST one JSON-RPC
// message, get the reply) and long-lived sessions (SSE).
//
// This unlocks the daemon model: one long-running `repowise serve`
// can host both the /api/* REST endpoints and /mcp, so multiple
// editor clients can attach without each spawning their own process.
func (s *Server) HTTPHandler() http.Handler {
	return server.NewStreamableHTTPServer(s.srv)
}

// ResolveRepositoryID returns the repository row's ID for the given local
// path, creating the row if needed. Use during CLI setup to translate a
// --repo flag into the ID that Options.RepositoryID needs.
func ResolveRepositoryID(ctx context.Context, db *sql.DB, localPath string) (string, error) {
	r, err := repos.New(db).EnsureByLocalPath(ctx, localPath, "")
	if err != nil {
		return "", err
	}
	return r.ID, nil
}

// resolveRepoID picks the repository ID for a single tool call. The
// resolution order:
//
//  1. The call's `repo` argument, if present. Tried as workspace
//     alias → full repo id → local path. The first match wins.
//  2. opts.RepositoryID, the construction-time default.
//
// Returns an error when neither is available (workspace-only mode +
// no `repo` arg supplied).
func (s *Server) resolveRepoID(ctx context.Context, req mcp.CallToolRequest) (string, error) {
	if spec := req.GetString("repo", ""); spec != "" {
		return s.resolveRepoSpec(ctx, spec)
	}
	if s.opts.RepositoryID != "" {
		return s.opts.RepositoryID, nil
	}
	return "", errors.New("missing required `repo` argument (server is in workspace-only mode)")
}

// resolveRepoSpec interprets one repo-pointing string in the order:
// alias from any registered workspace, full repository id, local
// filesystem path. Read-only — does not create new repository rows.
func (s *Server) resolveRepoSpec(ctx context.Context, spec string) (string, error) {
	// Alias from any registered workspace — first match wins.
	for _, root := range s.opts.workspaceRoots() {
		state, err := workspace.Load(root)
		if err != nil {
			continue
		}
		if entry, ok := state.Lookup(spec); ok {
			return s.repoIDForPath(ctx, entry.Path)
		}
	}
	// Full repository id.
	var id string
	if err := s.opts.DB.QueryRowContext(ctx,
		`SELECT id FROM repositories WHERE id = ?`, spec).Scan(&id); err == nil {
		return id, nil
	}
	// Local filesystem path.
	if abs, err := filepath.Abs(spec); err == nil {
		if rid, err := s.repoIDForPath(ctx, abs); err == nil {
			return rid, nil
		}
	}
	return "", fmt.Errorf("could not resolve %q (tried workspace alias, repository id, local path)", spec)
}

// repoIDForPath is a read-only lookup — returns the row id for the
// given absolute path, or an error if no row exists. Distinct from
// repos.EnsureByLocalPath, which would CREATE a row on miss.
func (s *Server) repoIDForPath(ctx context.Context, absPath string) (string, error) {
	var id string
	err := s.opts.DB.QueryRowContext(ctx,
		`SELECT id FROM repositories WHERE local_path = ?`, absPath).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("no repository indexed at %s", absPath)
		}
		return "", err
	}
	return id, nil
}
