// Package http hosts vor's HTTP API. The Server type wires the chi
// router, middleware, and route packages into one net/http handler.
// CLI / tests construct one via New and either ListenAndServe it directly
// or use it as an http.Handler (for httptest).
package http

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
	"github.com/HundredAcreStudio/vor/internal/server/http/routes"
	"github.com/HundredAcreStudio/vor/internal/server/registry"
	"github.com/HundredAcreStudio/vor/internal/version"
	"github.com/HundredAcreStudio/vor/ui"
)

// Options configures Server.
type Options struct {
	// DB is the open *sql.DB the route packages query.
	DB *sql.DB

	// Logger is the structured logger used by middleware. Defaults to
	// slog.Default() when nil.
	Logger *slog.Logger

	// Addr is the host:port to bind (only used by ListenAndServe).
	Addr string

	// ReadTimeout / WriteTimeout / IdleTimeout are forwarded to
	// http.Server. Zero leaves stdlib defaults.
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration

	// MCPHandler is the optional MCP server handler. When set, it's
	// mounted at /mcp so editor clients can connect over HTTP
	// (Streamable HTTP transport) instead of spawning a stdio process.
	// Nil disables the route.
	MCPHandler http.Handler

	// Registrar powers the live /api/repos/register|unregister endpoints.
	// Nil leaves those endpoints returning 503.
	Registrar *registry.Registrar
}

// Server is the HTTP API for vor. Implements http.Handler so it can
// be used directly with httptest.NewServer.
type Server struct {
	opts    Options
	handler http.Handler
}

// New constructs a Server with the supplied options. Routes are wired
// immediately so test code can hit the handler without ListenAndServe.
func New(opts Options) (*Server, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("Options.DB is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	r := chi.NewRouter()
	r.Use(httpx.Recovery(opts.Logger))
	r.Use(httpx.Logging(opts.Logger))

	// Service health (NB: distinct from code health, which is per repo).
	r.Get("/api/health", healthHandler())

	// Per-domain route packages mount themselves on /api.
	deps := routes.Deps{DB: opts.DB, Logger: opts.Logger, Registrar: opts.Registrar}
	r.Route("/api", func(api chi.Router) {
		routes.MountOverview(api, deps)
		routes.MountGlobalSettings(api, deps)
		api.Route("/repos", func(reposR chi.Router) {
			routes.MountRepos(reposR, deps)
			reposR.Route("/{repoID}", func(per chi.Router) {
				routes.MountRepoDetail(per, deps)
				routes.MountSettings(per, deps)
				routes.MountAttention(per, deps)
				routes.MountLanguages(per, deps)
				routes.MountInsights(per, deps)
				routes.MountArchitecture(per, deps)
				routes.MountFlows(per, deps)
				routes.MountGraph(per, deps)
				routes.MountSymbols(per, deps)
				routes.MountSearch(per, deps)
				routes.MountHotspots(per, deps)
				routes.MountDeadCode(per, deps)
				routes.MountHealth(per, deps)
				routes.MountExternals(per, deps)
				routes.MountDecisions(per, deps)
				routes.MountPages(per, deps)
				routes.MountTasks(per, deps)
				routes.MountPipeline(per, deps)
			})
		})
	})

	// MCP over Streamable HTTP. Mounted at /mcp so editor clients
	// connecting to the long-running daemon get the full vor
	// tool surface without each spawning a stdio child process.
	// The mcp-go handler manages session state, SSE upgrades, and
	// JSON-RPC parsing itself — we just hand off the chi prefix.
	if opts.MCPHandler != nil {
		r.Mount("/mcp", opts.MCPHandler)
	}

	// Dashboard SPA (embedded Vite build). Lowest-priority catch-all, so
	// /api and /mcp above still match first. Unknown paths fall back to
	// index.html for client-side routing.
	r.Handle("/*", ui.Handler())

	return &Server{opts: opts, handler: r}, nil
}

// ServeHTTP makes Server an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// ListenAndServe binds to opts.Addr and serves until ctx is cancelled.
// Returns the error from Shutdown if any.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:         s.opts.Addr,
		Handler:      s.handler,
		ReadTimeout:  s.opts.ReadTimeout,
		WriteTimeout: s.opts.WriteTimeout,
		IdleTimeout:  s.opts.IdleTimeout,
	}
	done := make(chan error, 1)
	go func() {
		s.opts.Logger.Info("server starting", "addr", s.opts.Addr)
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			done <- err
			return
		}
		done <- nil
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return <-done
	case err := <-done:
		return err
	}
}

// healthHandler is the service-level liveness check. Always returns
// 200 with the version stamp so monitoring can detect crashes / version
// rollouts.
func healthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"status":    "ok",
			"version":   version.Current(),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	}
}
