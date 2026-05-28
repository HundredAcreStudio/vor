// Package externalstore writes external.Record snapshots into the
// external_systems table. As with graphstore, ReplaceAll wipes the
// repository's existing rows and inserts the current set in one
// transaction.
package externalstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/HundredAcreStudio/vor/internal/ingestion/external"
)

// Store writes external.Record sets to the database.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// ReplaceAll deletes the existing external_systems rows for repoID and
// writes records in their place. Transactional.
func (s *Store) ReplaceAll(ctx context.Context, repoID string, records []external.Record) error {
	if repoID == "" {
		return fmt.Errorf("repoID is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM external_systems WHERE repository_id = ?`, repoID); err != nil {
		return fmt.Errorf("delete external_systems: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO external_systems (
		repository_id, name, display_name, ecosystem, category,
		version, declared_in, is_dev_dep
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, r := range records {
		var version interface{}
		if r.Version != "" {
			version = r.Version
		}
		if _, err := stmt.ExecContext(ctx,
			repoID, r.Name, r.DisplayName, r.Ecosystem, r.Category,
			version, r.DeclaredIn, boolToInt(r.IsDevDep),
		); err != nil {
			return fmt.Errorf("insert external_systems row %s/%s: %w", r.Ecosystem, r.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

// Count returns the number of external_systems rows for repoID.
func (s *Store) Count(ctx context.Context, repoID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM external_systems WHERE repository_id = ?`, repoID).Scan(&n)
	return n, err
}

// CountByEcosystem groups external_systems by ecosystem.
func (s *Store) CountByEcosystem(ctx context.Context, repoID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ecosystem, COUNT(*) FROM external_systems WHERE repository_id = ? GROUP BY ecosystem`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var eco string
		var n int
		if err := rows.Scan(&eco, &n); err != nil {
			return nil, err
		}
		out[eco] = n
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
