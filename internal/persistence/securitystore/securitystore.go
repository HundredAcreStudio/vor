// Package securitystore persists security.Finding records into
// security_findings. ReplaceAll wipes the repository's previous scan so a
// re-scan is idempotent. The id column is database-assigned (autoincrement
// / serial), so inserts omit it.
package securitystore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/repowise-dev/repowise-go/internal/analysis/security"
)

// Store persists security findings.
type Store struct{ db *sql.DB }

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// ReplaceAll deletes the repo's prior findings and inserts the new set in
// one transaction.
func (s *Store) ReplaceAll(ctx context.Context, repoID string, findings []security.Finding) error {
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

	if _, err := tx.ExecContext(ctx, `DELETE FROM security_findings WHERE repository_id = ?`, repoID); err != nil {
		return fmt.Errorf("delete security_findings: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO security_findings (
		repository_id, file_path, kind, severity, snippet, line_number
	) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()
	for _, f := range findings {
		var ln any
		if f.Line > 0 {
			ln = f.Line
		}
		var snip any
		if f.Snippet != "" {
			snip = f.Snippet
		}
		if _, err := stmt.ExecContext(ctx, repoID, f.FilePath, f.Kind, f.Severity, snip, ln); err != nil {
			return fmt.Errorf("insert finding: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

// List returns all findings for the repo, ordered by severity then file.
func (s *Store) List(ctx context.Context, repoID string) ([]security.Finding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT file_path, kind, severity, COALESCE(snippet, ''), COALESCE(line_number, 0)
		FROM security_findings WHERE repository_id = ?
		ORDER BY CASE severity
			WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END,
			file_path, line_number`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []security.Finding
	for rows.Next() {
		var f security.Finding
		if err := rows.Scan(&f.FilePath, &f.Kind, &f.Severity, &f.Snippet, &f.Line); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Count returns the number of stored findings for the repo.
func (s *Store) Count(ctx context.Context, repoID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM security_findings WHERE repository_id = ?`, repoID).Scan(&n)
	return n, err
}
