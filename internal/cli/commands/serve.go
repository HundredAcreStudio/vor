package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/config"
	"github.com/repowise-dev/repowise-go/internal/logging"
	rhttp "github.com/repowise-dev/repowise-go/internal/server/http"
)

// newServeCmd starts the HTTP API server. MCP transport lands in a
// follow-up commit; this command surface is shaped so adding it doesn't
// rename anything.
func newServeCmd() *cobra.Command {
	var (
		repoPath string
		addr     string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API server",
		Long: `Starts an HTTP server that exposes repowise's analysis output
(graph nodes/edges, hotspots, dead code, code health, externals) at
/api/repos/{id}/*. The server reads from the SQLite/Postgres database
configured via REPOWISE_DB_URL or .repowise/config.yaml.

The MCP transport (stdio for Claude Code / Cursor) lands in a follow-up.`,
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

			srv, err := rhttp.New(rhttp.Options{
				DB:           conn,
				Logger:       logger,
				Addr:         bind,
				ReadTimeout:  10 * time.Second,
				WriteTimeout: 30 * time.Second,
				IdleTimeout:  60 * time.Second,
			})
			if err != nil {
				return err
			}

			if err := srv.ListenAndServe(ctx); err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (used to resolve config + default DB)")
	cmd.Flags().StringVar(&addr, "addr", "", "host:port to bind (overrides REPOWISE_HOST/PORT)")
	return cmd
}
