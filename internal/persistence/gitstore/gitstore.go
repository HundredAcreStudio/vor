// Package gitstore persists git.PerFile records into the git_metadata
// table. As elsewhere, ReplaceAll wipes the repo's previous snapshot.
package gitstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/HundredAcreStudio/vor/internal/ingestion/git"
)

// Store writes git.PerFile records to the database.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// ReplaceAll deletes the existing git_metadata rows for repoID and inserts
// records in their place. Transactional.
func (s *Store) ReplaceAll(ctx context.Context, repoID string, records []git.PerFile) error {
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

	if _, err := tx.ExecContext(ctx, `DELETE FROM git_metadata WHERE repository_id = ?`, repoID); err != nil {
		return fmt.Errorf("delete git_metadata: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO git_metadata (
		id, repository_id, file_path,
		commit_count_total, commit_count_90d, commit_count_30d, commit_count_capped,
		first_commit_at, last_commit_at,
		primary_owner_name, primary_owner_email, primary_owner_commit_pct,
		recent_owner_name, recent_owner_commit_pct,
		top_authors_json, significant_commits_json, co_change_partners_json, commit_categories_json,
		is_hotspot, is_stable, churn_percentile, age_days,
		lines_added_90d, lines_deleted_90d, avg_commit_size,
		bus_factor, contributor_count
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, r := range records {
		topAuthors, err := json.Marshal(r.TopAuthors)
		if err != nil {
			return fmt.Errorf("marshal top_authors: %w", err)
		}
		significant := r.SignificantCommits
		if significant == nil {
			significant = []string{}
		}
		sigJSON, err := json.Marshal(significant)
		if err != nil {
			return fmt.Errorf("marshal significant_commits: %w", err)
		}
		coChange, err := json.Marshal(r.CoChangePartners)
		if err != nil {
			return fmt.Errorf("marshal co_change_partners: %w", err)
		}

		var (
			primaryName, primaryEmail interface{}
			primaryPct, recentPct     interface{}
			recentName                interface{}
		)
		if r.PrimaryOwner != nil {
			primaryName = r.PrimaryOwner.Name
			primaryEmail = r.PrimaryOwner.Email
			primaryPct = r.PrimaryOwner.CommitPct
		}
		if r.RecentOwner != nil {
			recentName = r.RecentOwner.Name
			recentPct = r.RecentOwner.CommitPct
		}

		var firstAt, lastAt interface{}
		if !r.FirstCommitAt.IsZero() {
			firstAt = r.FirstCommitAt
		}
		if !r.LastCommitAt.IsZero() {
			lastAt = r.LastCommitAt
		}

		if _, err := stmt.ExecContext(ctx,
			newID(), repoID, r.Path,
			r.CommitCountTotal, r.CommitCount90d, r.CommitCount30d, 0,
			firstAt, lastAt,
			primaryName, primaryEmail, primaryPct,
			recentName, recentPct,
			string(topAuthors), string(sigJSON), string(coChange), "{}",
			boolToInt(r.IsHotspot), boolToInt(r.IsStable), r.ChurnPercentile, r.AgeDays,
			r.LinesAdded90d, r.LinesDeleted90d, r.AvgCommitSize,
			r.BusFactor, r.ContributorCount,
		); err != nil {
			return fmt.Errorf("insert git_metadata for %s: %w", r.Path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

// ReplaceCommitCategories overwrites the repo's commit-category tally.
func (s *Store) ReplaceCommitCategories(ctx context.Context, repoID string, categories map[string]int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM commit_categories WHERE repository_id = ?`, repoID); err != nil {
		return err
	}
	for category, count := range categories {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO commit_categories (repository_id, category, count) VALUES (?, ?, ?)`,
			repoID, category, count); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Count returns the number of git_metadata rows for repoID.
func (s *Store) Count(ctx context.Context, repoID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM git_metadata WHERE repository_id = ?`, repoID).Scan(&n)
	return n, err
}

// LoadAll reconstructs the per-file git records needed by downstream phases
// (path, hotspot flag, co-change partners) from the stored git_metadata. Used
// to skip the commit-history walk when HEAD is unchanged: the git signals are
// commit-boundary, so the stored rows are still valid. Only the fields the
// health phase consumes are populated.
func (s *Store) LoadAll(ctx context.Context, repoID string) ([]git.PerFile, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT file_path, is_hotspot, COALESCE(co_change_partners_json, '')
		 FROM git_metadata WHERE repository_id = ?`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []git.PerFile
	for rows.Next() {
		var path, coJSON string
		var hot int
		if err := rows.Scan(&path, &hot, &coJSON); err != nil {
			return nil, err
		}
		pf := git.PerFile{Path: path, IsHotspot: hot != 0}
		if coJSON != "" {
			_ = json.Unmarshal([]byte(coJSON), &pf.CoChangePartners)
		}
		out = append(out, pf)
	}
	return out, rows.Err()
}

// Hotspots returns the file paths flagged as hotspots, ordered by churn
// percentile descending. Limit caps the result.
func (s *Store) Hotspots(ctx context.Context, repoID string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT file_path FROM git_metadata
		 WHERE repository_id = ? AND is_hotspot = 1
		 ORDER BY churn_percentile DESC, file_path
		 LIMIT ?`, repoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
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
