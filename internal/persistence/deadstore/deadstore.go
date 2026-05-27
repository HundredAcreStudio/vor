// Package deadstore writes deadcode.Finding records into the
// dead_code_findings table.
package deadstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/repowise-dev/repowise-go/internal/analysis/deadcode"
)

// Store persists deadcode findings.
type Store struct{ db *sql.DB }

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// ReplaceAll wipes the repository's existing dead_code_findings rows and
// writes findings in their place. Transactional.
func (s *Store) ReplaceAll(ctx context.Context, repoID string, findings []deadcode.Finding) error {
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

	if _, err := tx.ExecContext(ctx, `DELETE FROM dead_code_findings WHERE repository_id = ?`, repoID); err != nil {
		return fmt.Errorf("delete dead_code_findings: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO dead_code_findings (
		id, repository_id, kind, file_path, symbol_name, symbol_kind,
		confidence, reason, evidence_json, safe_to_delete, status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '[]', ?, 'open')`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, f := range findings {
		var symName, symKind interface{}
		if f.SymbolName != "" {
			symName = f.SymbolName
		}
		if f.SymbolKind != "" {
			symKind = f.SymbolKind
		}
		if _, err := stmt.ExecContext(ctx,
			newID(), repoID, string(f.Kind), f.FilePath, symName, symKind,
			f.Confidence, f.Reason, boolToInt(f.SafeToDelete),
		); err != nil {
			return fmt.Errorf("insert dead_code_findings (%s): %w", f.FilePath, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

// Count returns the number of dead_code_findings rows for repoID.
func (s *Store) Count(ctx context.Context, repoID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dead_code_findings WHERE repository_id = ?`, repoID).Scan(&n)
	return n, err
}

// SafeToDelete returns findings flagged safe_to_delete for the repo,
// ordered by confidence descending. limit caps the result; 0 means no
// cap (uses 100 as a guard).
func (s *Store) SafeToDelete(ctx context.Context, repoID string, limit int) ([]deadcode.Finding, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT kind, file_path, COALESCE(symbol_name,''), COALESCE(symbol_kind,''),
		        confidence, reason, safe_to_delete
		 FROM dead_code_findings
		 WHERE repository_id = ? AND safe_to_delete = 1
		 ORDER BY confidence DESC, file_path
		 LIMIT ?`, repoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []deadcode.Finding
	for rows.Next() {
		var f deadcode.Finding
		var safe int
		var kind string
		if err := rows.Scan(&kind, &f.FilePath, &f.SymbolName, &f.SymbolKind,
			&f.Confidence, &f.Reason, &safe); err != nil {
			return nil, err
		}
		f.Kind = deadcode.Kind(kind)
		f.SafeToDelete = safe == 1
		out = append(out, f)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func newID() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")
}
