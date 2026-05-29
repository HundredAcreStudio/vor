// Package settingsstore reads and writes vor's database-backed
// configuration. Each row is a JSON-encoded value scoped to a repository
// (repository_id = the repo's id) or to the global default (repository_id =
// "" — the Global sentinel). Resolution is repo-row → global-row → built-in
// default; that final fallback lives in the config package, not here.
package settingsstore

import (
	"context"
	"database/sql"
)

// Global is the sentinel repository_id for settings that apply to every repo
// unless a per-repo row overrides them.
const Global = ""

// Store is a thin data-access type over the settings table.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by conn.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Get returns the raw JSON value for key at the given scope. repoID may be
// Global. ok is false when no row exists at that exact scope (callers do
// their own repo→global fallback via GetScope, or use Resolve in config).
func (s *Store) Get(ctx context.Context, repoID, key string) (value string, ok bool, err error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE repository_id = ? AND key = ?`, repoID, key)
	switch err := row.Scan(&value); err {
	case nil:
		return value, true, nil
	case sql.ErrNoRows:
		return "", false, nil
	default:
		return "", false, err
	}
}

// GetScope returns every key→JSON value for a single scope (one repoID, or
// Global). Callers merge global + repo scopes themselves.
func (s *Store) GetScope(ctx context.Context, repoID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value FROM settings WHERE repository_id = ? ORDER BY key`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// Set upserts a JSON-encoded value at the given scope. ON CONFLICT works on
// both SQLite and Postgres for the (repository_id, key) primary key.
func (s *Store) Set(ctx context.Context, repoID, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (repository_id, key, value)
		 VALUES (?, ?, ?)
		 ON CONFLICT (repository_id, key)
		 DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		repoID, key, value)
	return err
}

// Delete removes a key at a scope (reverting it to the next layer down:
// repo→global→built-in default). No error if the row was already absent.
func (s *Store) Delete(ctx context.Context, repoID, key string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM settings WHERE repository_id = ? AND key = ?`, repoID, key)
	return err
}
