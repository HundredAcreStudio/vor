// Package pipelinestore writes phase-execution rows into the pipeline_jobs
// table. Each call to Begin allocates a row that StartPhase / FinishPhase /
// FailPhase mutate; Finish stamps the terminal state.
//
// The pipeline package uses this layer to surface "what phase is running
// for repo X right now" via the persistence layer — useful for observability
// and the future resume path (Pass B).
package pipelinestore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Job tracks one phase execution for one repository. A pipeline run is a
// sequence of Jobs, one per phase.
type Job struct {
	ID           string
	RepositoryID string
	Phase        string
	State        string
	Cursor       string
	StartedAt    time.Time
	UpdatedAt    time.Time
	Error        string
	MetadataJSON string
}

// State enumerates the pipeline_jobs.state column.
const (
	StatePending   = "pending"
	StateRunning   = "running"
	StateCompleted = "completed"
	StateFailed    = "failed"
)

// Store writes pipeline_jobs rows.
type Store struct{ db *sql.DB }

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Begin inserts a new pipeline_jobs row in state=pending and returns the
// freshly-allocated Job. Callers should call Start when the phase actually
// begins.
func (s *Store) Begin(ctx context.Context, repoID, phase string) (*Job, error) {
	if repoID == "" || phase == "" {
		return nil, fmt.Errorf("repoID and phase are required")
	}
	job := &Job{
		ID:           newID(),
		RepositoryID: repoID,
		Phase:        phase,
		State:        StatePending,
		MetadataJSON: "{}",
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO pipeline_jobs (id, repository_id, phase, state, metadata_json)
		 VALUES (?, ?, ?, ?, '{}')`,
		job.ID, repoID, phase, StatePending)
	if err != nil {
		return nil, fmt.Errorf("insert pipeline_jobs: %w", err)
	}
	return job, nil
}

// Start flips a job to state=running and bumps updated_at.
func (s *Store) Start(ctx context.Context, jobID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE pipeline_jobs SET state = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		StateRunning, jobID)
	if err != nil {
		return fmt.Errorf("start job: %w", err)
	}
	return nil
}

// Complete flips a job to state=completed and clears any prior error.
func (s *Store) Complete(ctx context.Context, jobID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE pipeline_jobs SET state = ?, error = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		StateCompleted, jobID)
	if err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	return nil
}

// Fail flips a job to state=failed and stamps the error message.
func (s *Store) Fail(ctx context.Context, jobID, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE pipeline_jobs SET state = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		StateFailed, errMsg, jobID)
	if err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	return nil
}

// LatestByRepo returns the most recent pipeline_jobs rows for a repository,
// ordered by started_at descending. limit ≤ 0 defaults to 50.
func (s *Store) LatestByRepo(ctx context.Context, repoID string, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, repository_id, phase, state, COALESCE(cursor,''), started_at, updated_at,
		        COALESCE(error,''), metadata_json
		 FROM pipeline_jobs WHERE repository_id = ?
		 ORDER BY started_at DESC, id DESC LIMIT ?`,
		repoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.RepositoryID, &j.Phase, &j.State, &j.Cursor,
			&j.StartedAt, &j.UpdatedAt, &j.Error, &j.MetadataJSON); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CountByState returns a state→count map for a repository. Useful for
// "any failed phases?" checks.
func (s *Store) CountByState(ctx context.Context, repoID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT state, COUNT(*) FROM pipeline_jobs WHERE repository_id = ? GROUP BY state`,
		repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		out[state] = n
	}
	return out, rows.Err()
}

func newID() string { return strings.ReplaceAll(uuid.New().String(), "-", "") }
