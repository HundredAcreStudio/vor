// Package mcp exposes repowise's analysis output via the Model Context
// Protocol. Tools are stdio-driven so editor integrations (Claude Code /
// Cursor) can read graph, git, dead-code, and health data without HTTP.
//
// The server is implicitly bound to one repository — configured at
// construction time — so tool calls don't need to ferry a repo ID.
package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/server"

	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/version"
)

// Options configures Server.
type Options struct {
	// DB is the open *sql.DB read by tool handlers.
	DB *sql.DB

	// RepositoryID is the row in repositories that all tool calls target.
	// Required.
	RepositoryID string

	// Logger receives structured tool-call logs. Defaults to slog.Default().
	Logger *slog.Logger
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
	if opts.RepositoryID == "" {
		return nil, fmt.Errorf("Options.RepositoryID is required")
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
