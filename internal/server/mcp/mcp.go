// Package mcp exposes vor's analysis output via the Model Context
// Protocol. Tools are stdio-driven so editor integrations (Claude Code /
// Cursor) can read graph, git, dead-code, and health data without HTTP.
//
// Repo targeting:
//
//   - Single-repo: Options.RepositoryID is the default for tool calls
//     that don't override via the `repo` argument.
//   - Daemon/registry: Options.Registrar is set and RepositoryID may be
//     empty; every tool call supplies a `repo` argument (full id or local
//     path) to pick the repo.
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

	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/providers"
	"github.com/HundredAcreStudio/vor/internal/server/registry"
	"github.com/HundredAcreStudio/vor/internal/version"
)

// Options configures Server.
type Options struct {
	// DB is the open *sql.DB read by tool handlers.
	DB *sql.DB

	// RepositoryID is the default repo for tool calls that don't
	// override via a `repo` argument. Optional when a Registrar is set
	// (registry mode), where callers address repos per-call.
	RepositoryID string

	// Provider powers the LLM-synthesis tools (get_answer, get_why).
	// Optional — when nil those tools degrade to returning the
	// retrieved raw context with a "synthesis unavailable" note rather
	// than erroring. get_context never needs it (pure data assembly).
	Provider providers.Provider

	// Model is passed through on synthesis requests. Empty lets the
	// provider pick its default.
	Model string

	// Embedder powers semantic search in vor_search. Optional —
	// when nil (or when no embeddings exist for the repo) the tool
	// falls back to the LIKE path over graph_nodes. Must match the
	// embedder used by `vor embed` for results to be meaningful.
	Embedder providers.Embedder

	// Logger receives structured tool-call logs. Defaults to slog.Default().
	Logger *slog.Logger

	// Registrar powers the vor_track / vor_untrack tools so an agent can
	// register the worktree it's working in. Nil disables those tools.
	Registrar *registry.Registrar
}

// Server wraps mark3labs/mcp-go's MCPServer with vor's tool surface.
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
	// A registrar-backed daemon may run with no default repo (registry
	// mode): clients address repos per-call via the `repo` argument.
	if opts.RepositoryID == "" && opts.Registrar == nil {
		return nil, fmt.Errorf("Options.RepositoryID or a Registrar is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	mcpSrv := server.NewMCPServer(
		"vor",
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
// This unlocks the daemon model: one long-running `vor serve`
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
//  1. The call's `repo` argument, if present. Tried as full repo id →
//     local path. The first match wins.
//  2. opts.RepositoryID, the construction-time default.
//
// Returns an error when neither is available (registry mode with no
// `repo` arg supplied).
func (s *Server) resolveRepoID(ctx context.Context, req mcp.CallToolRequest) (string, error) {
	if spec := req.GetString("repo", ""); spec != "" {
		return s.resolveRepoSpec(ctx, spec)
	}
	if s.opts.RepositoryID != "" {
		return s.opts.RepositoryID, nil
	}
	return "", errors.New("missing required `repo` argument (no default repo configured)")
}

// resolveRepoSpec interprets one repo-pointing string as a full repository
// id first, then a local filesystem path. Read-only — does not create new
// repository rows.
func (s *Server) resolveRepoSpec(ctx context.Context, spec string) (string, error) {
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
	return "", fmt.Errorf("could not resolve %q (tried repository id and local path)", spec)
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
