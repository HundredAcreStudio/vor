// Package coveragestore persists parsed test-coverage into coverage_files
// and exposes a per-file line-coverage map for the untested_hotspot
// biomarker. Upsert is keyed by (repository_id, file_path) so re-importing
// a fresh report replaces the prior numbers for each file.
package coveragestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/repowise-dev/repowise-go/internal/analysis/health/coverage"
)

// Store persists coverage records.
type Store struct{ db *sql.DB }

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Upsert writes one row per file, replacing any prior coverage for the same
// (repo, path). commitSHA is optional provenance ("" stores NULL).
func (s *Store) Upsert(ctx context.Context, repoID, commitSHA string, files []coverage.FileCoverage) error {
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

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO coverage_files (
		id, repository_id, file_path, source_format, line_coverage_pct,
		branch_coverage_pct, covered_lines_json, total_coverable_lines, ingested_commit_sha
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(repository_id, file_path) DO UPDATE SET
		source_format = excluded.source_format,
		line_coverage_pct = excluded.line_coverage_pct,
		branch_coverage_pct = excluded.branch_coverage_pct,
		covered_lines_json = excluded.covered_lines_json,
		total_coverable_lines = excluded.total_coverable_lines,
		ingested_at = CURRENT_TIMESTAMP,
		ingested_commit_sha = excluded.ingested_commit_sha`)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	var sha any
	if commitSHA != "" {
		sha = commitSHA
	}
	for _, f := range files {
		lines := f.CoveredLines
		if lines == nil {
			lines = []int{}
		}
		raw, err := json.Marshal(lines)
		if err != nil {
			return fmt.Errorf("marshal covered lines: %w", err)
		}
		var branch any
		if f.BranchPct != nil {
			branch = *f.BranchPct
		}
		if _, err := stmt.ExecContext(ctx,
			uuid.NewString(), repoID, f.Path, string(f.Format), f.LinePct,
			branch, string(raw), f.TotalCoverable, sha,
		); err != nil {
			return fmt.Errorf("upsert coverage for %s: %w", f.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

// CoverageMap returns file_path -> line_coverage_pct for the repo.
func (s *Store) CoverageMap(ctx context.Context, repoID string) (map[string]float64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT file_path, line_coverage_pct FROM coverage_files WHERE repository_id = ?`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var path string
		var pct float64
		if err := rows.Scan(&path, &pct); err != nil {
			return nil, err
		}
		out[path] = pct
	}
	return out, rows.Err()
}

// Count returns the number of files with stored coverage for the repo.
func (s *Store) Count(ctx context.Context, repoID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM coverage_files WHERE repository_id = ?`, repoID).Scan(&n)
	return n, err
}
