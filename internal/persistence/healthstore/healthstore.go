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

// Load reconstructs the stored health result (findings + file metrics) for a
// repo — the "prior" run fed to health.AnalyzeIncremental so unchanged files'
// file-local findings can be reused.
func (s *Store) Load(ctx context.Context, repoID string) (health.Result, error) {
	var res health.Result

	frows, err := s.db.QueryContext(ctx, `
		SELECT file_path, biomarker_type, severity, COALESCE(function_name,''),
		       COALESCE(line_start,0), COALESCE(line_end,0), health_impact,
		       COALESCE(reason,''), COALESCE(details_json,'')
		FROM health_findings WHERE repository_id = ?`, repoID)
	if err != nil {
		return res, err
	}
	defer frows.Close()
	for frows.Next() {
		var f health.Finding
		var sev, details string
		if err := frows.Scan(&f.FilePath, &f.BiomarkerType, &sev, &f.FunctionName,
			&f.LineStart, &f.LineEnd, &f.HealthImpact, &f.Reason, &details); err != nil {
			return res, err
		}
		f.Severity = health.Severity(sev)
		if details != "" && details != "{}" {
			_ = json.Unmarshal([]byte(details), &f.Details)
		}
		res.Findings = append(res.Findings, f)
	}
	if err := frows.Err(); err != nil {
		return res, err
	}

	mrows, err := s.db.QueryContext(ctx, `
		SELECT file_path, score, max_ccn, max_nesting, nloc, has_test_file, COALESCE(module,'')
		FROM health_file_metrics WHERE repository_id = ?`, repoID)
	if err != nil {
		return res, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var m health.FileMetric
		var hasTest int
		if err := mrows.Scan(&m.FilePath, &m.Score, &m.MaxCCN, &m.MaxNesting, &m.NLOC, &hasTest, &m.Module); err != nil {
			return res, err
		}
		m.HasTestFile = hasTest != 0
		res.FileMetrics = append(res.FileMetrics, m)
	}
	return res, mrows.Err()
}

// ApplyIncremental writes an incrementally-computed health result without the
// full ReplaceAll wipe: it rewrites only the changed files' findings, all
// global (cross-file) findings, and removed files' rows — leaving unchanged
// files' file-local finding rows in place. Metrics are small (one row per
// file) so they're fully replaced. result is the FULL result (e.g. from
// health.AnalyzeIncremental); changed is the set of files re-analyzed.
func (s *Store) ApplyIncremental(ctx context.Context, repoID string, result health.Result, changed map[string]bool) error {
	if repoID == "" {
		return fmt.Errorf("repoID is required")
	}
	current := make(map[string]bool, len(result.FileMetrics))
	for _, m := range result.FileMetrics {
		current[m.FilePath] = true
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

	// Clear findings for files no longer present (removed), and for changed
	// files (their file-local set is rewritten below).
	existing, err := distinctFindingPaths(ctx, tx, repoID)
	if err != nil {
		return err
	}
	toDelete := map[string]bool{}
	for _, p := range existing {
		if !current[p] || changed[p] {
			toDelete[p] = true
		}
	}
	for p := range toDelete {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM health_findings WHERE repository_id = ? AND file_path = ?`, repoID, p); err != nil {
			return fmt.Errorf("delete findings for %s: %w", p, err)
		}
	}
	// Clear all global (cross-file) findings — they're recomputed in full.
	for _, bm := range health.GlobalBiomarkers() {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM health_findings WHERE repository_id = ? AND biomarker_type = ?`, repoID, bm); err != nil {
			return fmt.Errorf("delete global findings %s: %w", bm, err)
		}
	}

	// Reinsert the findings we cleared: global ones, plus changed files'
	// file-local ones. Unchanged files' file-local findings stay untouched.
	var toInsert []health.Finding
	for _, f := range result.Findings {
		if changed[f.FilePath] || !health.IsFileLocalBiomarker(f.BiomarkerType) {
			toInsert = append(toInsert, f)
		}
	}
	if err := insertFindings(ctx, tx, repoID, toInsert); err != nil {
		return err
	}

	// Metrics: full replace (one row per file, cheap, and a cross-file finding
	// change can shift any file's score).
	if _, err := tx.ExecContext(ctx, `DELETE FROM health_file_metrics WHERE repository_id = ?`, repoID); err != nil {
		return fmt.Errorf("delete health_file_metrics: %w", err)
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

func distinctFindingPaths(ctx context.Context, tx *sql.Tx, repoID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT file_path FROM health_findings WHERE repository_id = ?`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
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

// FileScores returns the current per-file health score (0–10) for every file
// with a metric row — the "current" side of a health diff against a baseline
// snapshot.
func (s *Store) FileScores(ctx context.Context, repoID string) (map[string]float64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT file_path, score FROM health_file_metrics WHERE repository_id = ?`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var path string
		var score float64
		if err := rows.Scan(&path, &score); err != nil {
			return nil, err
		}
		out[path] = score
	}
	return out, rows.Err()
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
