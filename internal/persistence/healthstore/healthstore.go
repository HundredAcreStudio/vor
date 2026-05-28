// Package healthstore writes health.Finding + health.FileMetric records
// into health_findings and health_file_metrics. ReplaceAll wipes the
// repository's previous snapshot.
package healthstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/HundredAcreStudio/vor/internal/analysis/health"
)

// Store persists health analyzer output.
type Store struct{ db *sql.DB }

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// ReplaceAll wipes both health_findings and health_file_metrics for repoID
// and inserts the new snapshot in a single transaction.
func (s *Store) ReplaceAll(ctx context.Context, repoID string, result health.Result) error {
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

	if _, err := tx.ExecContext(ctx, `DELETE FROM health_findings WHERE repository_id = ?`, repoID); err != nil {
		return fmt.Errorf("delete health_findings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM health_file_metrics WHERE repository_id = ?`, repoID); err != nil {
		return fmt.Errorf("delete health_file_metrics: %w", err)
	}

	if err := insertFindings(ctx, tx, repoID, result.Findings); err != nil {
		return err
	}
	if err := insertMetrics(ctx, tx, repoID, result.FileMetrics); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

func insertFindings(ctx context.Context, tx *sql.Tx, repoID string, findings []health.Finding) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO health_findings (
		id, repository_id, file_path, biomarker_type, severity,
		function_name, line_start, line_end, details_json, health_impact, reason
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare finding insert: %w", err)
	}
	defer stmt.Close()
	for _, f := range findings {
		var fn, ls, le interface{}
		if f.FunctionName != "" {
			fn = f.FunctionName
		}
		if f.LineStart > 0 {
			ls = f.LineStart
		}
		if f.LineEnd > 0 {
			le = f.LineEnd
		}
		details := f.Details
		if details == nil {
			details = map[string]any{}
		}
		raw, err := json.Marshal(details)
		if err != nil {
			return fmt.Errorf("marshal details: %w", err)
		}
		if _, err := stmt.ExecContext(ctx,
			newID(), repoID, f.FilePath, f.BiomarkerType, string(f.Severity),
			fn, ls, le, string(raw), f.HealthImpact, f.Reason,
		); err != nil {
			return fmt.Errorf("insert health_finding: %w", err)
		}
	}
	return nil
}

func insertMetrics(ctx context.Context, tx *sql.Tx, repoID string, metrics []health.FileMetric) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO health_file_metrics (
		id, repository_id, file_path, score, max_ccn, max_nesting, nloc,
		duplication_pct, has_test_file, line_coverage_pct, branch_coverage_pct, module
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare metric insert: %w", err)
	}
	defer stmt.Close()
	for _, m := range metrics {
		var mod interface{}
		if m.Module != "" {
			mod = m.Module
		}
		if _, err := stmt.ExecContext(ctx,
			newID(), repoID, m.FilePath, m.Score, m.MaxCCN, m.MaxNesting, m.NLOC,
			nil, boolToInt(m.HasTestFile), nil, nil, mod,
		); err != nil {
			return fmt.Errorf("insert health_file_metrics: %w", err)
		}
	}
	return nil
}

// CountFindings returns the number of health_findings rows for repoID.
func (s *Store) CountFindings(ctx context.Context, repoID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM health_findings WHERE repository_id = ?`, repoID).Scan(&n)
	return n, err
}

// CountByBiomarker tallies health_findings rows per biomarker_type.
func (s *Store) CountByBiomarker(ctx context.Context, repoID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT biomarker_type, COUNT(*) FROM health_findings WHERE repository_id = ? GROUP BY biomarker_type`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, rows.Err()
}

// AverageScore returns the mean file score across all health_file_metrics
// rows. Returns 10.0 (perfect) for an empty repository.
func (s *Store) AverageScore(ctx context.Context, repoID string) (float64, error) {
	var avg sql.NullFloat64
	err := s.db.QueryRowContext(ctx,
		`SELECT AVG(score) FROM health_file_metrics WHERE repository_id = ?`, repoID).Scan(&avg)
	if err != nil {
		return 0, err
	}
	if !avg.Valid {
		return 10.0, nil
	}
	return avg.Float64, nil
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
