// Package db opens database/sql connections from a repowise database URL,
// picking the right driver for SQLite or PostgreSQL.
//
// URL forms supported:
//   - sqlite:/relative/path.db
//   - sqlite:///absolute/path.db
//   - sqlite+aiosqlite:///absolute/path.db   (legacy from Python config)
//   - file:foo.db                            (raw sqlite path)
//   - postgres://user:pw@host/db?sslmode=...
//   - postgresql://user:pw@host/db
//
// The Python project uses URLs with the SQLAlchemy "sqlite+aiosqlite" scheme;
// we recognise it for compatibility so users porting an existing .env don't
// have to edit it.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	// Pure-Go sqlite driver. Registers itself as "sqlite".
	_ "modernc.org/sqlite"

	// pgx stdlib driver. Registers itself as "pgx".
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Dialect identifies the database engine.
type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

// OpenOptions configures Open.
type OpenOptions struct {
	URL string

	// MaxOpenConns and MaxIdleConns leave SQL defaults if zero.
	MaxOpenConns int
	MaxIdleConns int

	// PingTimeout caps Open's verification step. Zero means 5s.
	PingTimeout time.Duration
}

// Open returns a verified *sql.DB along with the detected dialect. The caller
// owns the *sql.DB and must Close it.
func Open(ctx context.Context, opts OpenOptions) (*sql.DB, Dialect, error) {
	driver, dsn, dialect, err := resolveDriverDSN(opts.URL)
	if err != nil {
		return nil, "", err
	}

	conn, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, "", fmt.Errorf("sql.Open(%s): %w", driver, err)
	}

	if opts.MaxOpenConns > 0 {
		conn.SetMaxOpenConns(opts.MaxOpenConns)
	}
	if opts.MaxIdleConns > 0 {
		conn.SetMaxIdleConns(opts.MaxIdleConns)
	}

	pingTO := opts.PingTimeout
	if pingTO <= 0 {
		pingTO = 5 * time.Second
	}
	pingCtx, cancel := context.WithTimeout(ctx, pingTO)
	defer cancel()
	if err := conn.PingContext(pingCtx); err != nil {
		_ = conn.Close()
		return nil, "", fmt.Errorf("ping %s: %w", dialect, err)
	}

	if dialect == DialectSQLite {
		if err := applySQLitePragmas(ctx, conn); err != nil {
			_ = conn.Close()
			return nil, "", err
		}
	}

	return conn, dialect, nil
}

// resolveDriverDSN normalises a repowise URL into (driver, DSN, dialect).
func resolveDriverDSN(rawURL string) (driver, dsn string, dialect Dialect, err error) {
	if rawURL == "" {
		return "", "", "", fmt.Errorf("empty database URL")
	}
	lower := strings.ToLower(rawURL)
	switch {
	case strings.HasPrefix(lower, "postgres://"),
		strings.HasPrefix(lower, "postgresql://"):
		return "pgx", rawURL, DialectPostgres, nil

	case strings.HasPrefix(lower, "sqlite+aiosqlite:///"):
		// SQLAlchemy form used by the Python project: keep the path after
		// the triple slash as an absolute filesystem path.
		return "sqlite", "file:" + strings.TrimPrefix(rawURL, "sqlite+aiosqlite:///"), DialectSQLite, nil
	case strings.HasPrefix(lower, "sqlite+aiosqlite://"):
		return "sqlite", "file:" + strings.TrimPrefix(rawURL, "sqlite+aiosqlite://"), DialectSQLite, nil

	case strings.HasPrefix(lower, "sqlite:///"):
		return "sqlite", "file:" + strings.TrimPrefix(rawURL, "sqlite:///"), DialectSQLite, nil
	case strings.HasPrefix(lower, "sqlite://"):
		return "sqlite", "file:" + strings.TrimPrefix(rawURL, "sqlite://"), DialectSQLite, nil
	case strings.HasPrefix(lower, "sqlite:"):
		return "sqlite", "file:" + strings.TrimPrefix(rawURL, "sqlite:"), DialectSQLite, nil

	case strings.HasPrefix(lower, "file:"):
		return "sqlite", rawURL, DialectSQLite, nil

	default:
		return "", "", "", fmt.Errorf("unrecognised database URL scheme: %q", rawURL)
	}
}

// applySQLitePragmas turns on the pragmas we want everywhere:
//   - foreign_keys ON (SQLite defaults to OFF, which silently allows orphans)
//   - journal_mode WAL (better concurrency for read-heavy workloads)
//   - busy_timeout 5s (avoid spurious "database is locked" on contention)
func applySQLitePragmas(ctx context.Context, conn *sql.DB) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	}
	for _, p := range pragmas {
		if _, err := conn.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("apply pragma %q: %w", p, err)
		}
	}
	return nil
}
