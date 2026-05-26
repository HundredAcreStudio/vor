// Package migrations runs the embedded SQL migrations against an open
// *sql.DB. Migrations live as .sql files under sqlite/ and postgres/; this
// package selects the right directory by dialect and drives goose.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"

	"github.com/repowise-dev/repowise-go/internal/persistence/db"
)

//go:embed sqlite/*.sql postgres/*.sql
var migrationFS embed.FS

// Up applies all pending migrations.
func Up(ctx context.Context, conn *sql.DB, dialect db.Dialect) error {
	prov, err := provider(conn, dialect)
	if err != nil {
		return err
	}
	return prov.Up(ctx, conn, ".")
}

// Down rolls back the most recent migration. Mainly used in tests.
func Down(ctx context.Context, conn *sql.DB, dialect db.Dialect) error {
	prov, err := provider(conn, dialect)
	if err != nil {
		return err
	}
	return prov.Down(ctx, conn, ".")
}

// Status returns the migration log entries.
func Status(ctx context.Context, conn *sql.DB, dialect db.Dialect) ([]*goose.MigrationStatus, error) {
	prov, err := provider(conn, dialect)
	if err != nil {
		return nil, err
	}
	return prov.Status(ctx, conn, ".")
}

// runner abstracts the goose surface we need so the per-dialect dispatch in
// Up/Down/Status stays single-source.
type runner struct {
	dialect string
	subFS   fs.FS
}

func provider(conn *sql.DB, dialect db.Dialect) (*runner, error) {
	if conn == nil {
		return nil, errors.New("nil *sql.DB")
	}
	var (
		gooseDialect string
		subdir       string
	)
	switch dialect {
	case db.DialectSQLite:
		gooseDialect, subdir = "sqlite3", "sqlite"
	case db.DialectPostgres:
		gooseDialect, subdir = "postgres", "postgres"
	default:
		return nil, fmt.Errorf("unsupported dialect: %q", dialect)
	}
	sub, err := fs.Sub(migrationFS, subdir)
	if err != nil {
		return nil, fmt.Errorf("locate %s migrations: %w", dialect, err)
	}
	return &runner{dialect: gooseDialect, subFS: sub}, nil
}

func (r *runner) Up(ctx context.Context, conn *sql.DB, dir string) error {
	goose.SetBaseFS(r.subFS)
	defer goose.SetBaseFS(nil)
	if err := goose.SetDialect(r.dialect); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	return goose.UpContext(ctx, conn, dir)
}

func (r *runner) Down(ctx context.Context, conn *sql.DB, dir string) error {
	goose.SetBaseFS(r.subFS)
	defer goose.SetBaseFS(nil)
	if err := goose.SetDialect(r.dialect); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	return goose.DownContext(ctx, conn, dir)
}

func (r *runner) Status(ctx context.Context, conn *sql.DB, dir string) ([]*goose.MigrationStatus, error) {
	goose.SetBaseFS(r.subFS)
	defer goose.SetBaseFS(nil)
	if err := goose.SetDialect(r.dialect); err != nil {
		return nil, fmt.Errorf("set dialect: %w", err)
	}
	migrations, err := goose.CollectMigrations(dir, 0, goose.MaxVersion)
	if err != nil {
		return nil, fmt.Errorf("collect migrations: %w", err)
	}
	out := make([]*goose.MigrationStatus, 0, len(migrations))
	for _, m := range migrations {
		out = append(out, &goose.MigrationStatus{
			Source: &goose.Source{
				Path:    m.Source,
				Version: m.Version,
			},
		})
	}
	return out, nil
}
