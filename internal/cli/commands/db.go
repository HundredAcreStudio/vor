package commands

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
)

func newDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Database management commands",
	}
	cmd.AddCommand(newDBMigrateCmd())
	cmd.AddCommand(newDBStatusCmd())
	return cmd
}

func newDBMigrateCmd() *cobra.Command {
	var repoPath string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run pending database migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			conn, dialect, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			if err := migrations.Up(ctx, conn, dialect); err != nil {
				return fmt.Errorf("apply migrations: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "migrations applied (%s)\n", dialect)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (resolves .vor/config.yaml and default wiki.db)")
	return cmd
}

func newDBStatusCmd() *cobra.Command {
	var repoPath string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show migration status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			conn, dialect, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			rows, err := migrations.Status(ctx, conn, dialect)
			if err != nil {
				return fmt.Errorf("status: %w", err)
			}
			for _, r := range rows {
				if r.Source == nil {
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%d\t%s\n", r.Source.Version, r.Source.Path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	return cmd
}

// openDB resolves the database URL from config (env > .vor/config.yaml >
// default sqlite://.vor/wiki.db) and opens the connection.
func openDB(ctx context.Context, repoPath string) (*sql.DB, db.Dialect, error) {
	cfg, err := config.Load(repoPath)
	if err != nil {
		return nil, "", fmt.Errorf("load config: %w", err)
	}

	url := cfg.DatabaseURL
	if url == "" {
		abs, err := filepath.Abs(filepath.Join(repoPath, ".vor", "wiki.db"))
		if err != nil {
			return nil, "", fmt.Errorf("resolve default db path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, "", fmt.Errorf("create .vor dir: %w", err)
		}
		url = "sqlite:" + abs
	}

	return db.Open(ctx, db.OpenOptions{URL: url})
}
