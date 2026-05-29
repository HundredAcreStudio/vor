package commands

import (
	"context"
	"database/sql"
	"fmt"

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
	cmd.Flags().StringVar(&repoPath, "repo", ".", "(unused; the global DB at ~/.config/vor/vor.db is always used unless VOR_DB_URL is set)")
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

// openDB opens the global vor database. The location is bootstrap-resolved
// (VOR_DB_URL / VOR_DATABASE_URL, else ~/.config/vor/vor.db) — one shared DB
// for every repo, not a per-repo .vor/wiki.db. repoPath is retained for
// call-site compatibility but no longer affects the database location.
func openDB(ctx context.Context, repoPath string) (*sql.DB, db.Dialect, error) {
	_ = repoPath
	boot := config.LoadBootstrap()
	// No migration here — the daemon migrates on startup, and the setup
	// commands (init, register, db migrate) migrate explicitly. Read/utility
	// commands operate on an already-migrated DB, so they just connect.
	return db.Open(ctx, db.OpenOptions{URL: boot.DatabaseURL})
}
