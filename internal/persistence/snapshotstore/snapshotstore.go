// Package snapshotstore persists point-in-time code-health snapshots, one per
// distinct indexed commit, into health_snapshots. Unlike the latest-only
// health_findings/health_file_metrics (which ReplaceAll wipes each run), these
// are append-only and form the repo's health-over-time history — the substrate
// for trend charts and commit-to-commit diffs.
package snapshotstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// Snapshot is one health measurement for a repo at a commit.
type Snapshot struct {
	ID            string
	RepositoryID  string
	TakenAt       time.Time
	CommitSHA     string
	Branch        string
	HotspotHealth float64
	AverageHealth float64
	WorstPath     string
	WorstScore    float64
	PerFileScores map[string]float64
}

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// Latest returns the most recent snapshot for the repo, or (nil, nil) when
// none exist.
func (s *Store) Latest(ctx context.Context, repoID string) (*Snapshot, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, repository_id, taken_at, commit_sha, branch,
		       hotspot_health, average_health,
		       COALESCE(worst_performer_path,''), COALESCE(worst_performer_score,0),
		       per_file_scores_json
		FROM health_snapshots WHERE repository_id = ?
		ORDER BY taken_at DESC LIMIT 1`, repoID)
	snap, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return snap, err
}

// Insert appends a snapshot. ID/TakenAt are filled when empty.
func (s *Store) Insert(ctx context.Context, snap Snapshot) error {
	if snap.ID == "" {
		snap.ID = newID()
	}
	if snap.TakenAt.IsZero() {
		snap.TakenAt = time.Now().UTC()
	}
	pf, err := json.Marshal(snap.PerFileScores)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO health_snapshots
		  (id, repository_id, taken_at, commit_sha, branch,
		   hotspot_health, average_health, worst_performer_path,
		   worst_performer_score, per_file_scores_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snap.ID, snap.RepositoryID, snap.TakenAt, snap.CommitSHA, snap.Branch,
		snap.HotspotHealth, snap.AverageHealth, snap.WorstPath, snap.WorstScore, string(pf))
	return err
}

// List returns the repo's snapshots in chronological order (oldest→newest),
// capped to the most recent limit.
func (s *Store) List(ctx context.Context, repoID string, limit int) ([]Snapshot, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, repository_id, taken_at, commit_sha, branch,
		       hotspot_health, average_health,
		       COALESCE(worst_performer_path,''), COALESCE(worst_performer_score,0),
		       per_file_scores_json
		FROM health_snapshots WHERE repository_id = ?
		ORDER BY taken_at DESC LIMIT ?`, repoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		snap, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *snap)
	}
	// Reverse to chronological order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// Prune keeps only the most recent keep snapshots per repo.
func (s *Store) Prune(ctx context.Context, repoID string, keep int) error {
	if keep <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM health_snapshots
		WHERE repository_id = ? AND id NOT IN (
		  SELECT id FROM health_snapshots WHERE repository_id = ?
		  ORDER BY taken_at DESC LIMIT ?)`, repoID, repoID, keep)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(row scanner) (*Snapshot, error) {
	var s Snapshot
	var pf string
	if err := row.Scan(&s.ID, &s.RepositoryID, &s.TakenAt, &s.CommitSHA, &s.Branch,
		&s.HotspotHealth, &s.AverageHealth, &s.WorstPath, &s.WorstScore, &pf); err != nil {
		return nil, err
	}
	if pf != "" {
		_ = json.Unmarshal([]byte(pf), &s.PerFileScores)
	}
	return &s, nil
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
