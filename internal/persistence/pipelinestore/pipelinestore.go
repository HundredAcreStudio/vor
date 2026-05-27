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
// begins. The runID groups every phase of one pipeline run so resume can
// stitch the attempt chain together; pass "" when the caller doesn't
// track runs.
func (s *Store) Begin(ctx context.Context, repoID, phase, runID string) (*Job, error) {
	if repoID == "" || phase == "" {
		return nil, fmt.Errorf("repoID and phase are required")
	}
	metadata := "{}"
	if runID != "" {
		metadata = fmt.Sprintf(`{"run_id":%q}`, runID)
	}
	job := &Job{
		ID:           newID(),
		RepositoryID: repoID,
		Phase:        phase,
		State:        StatePending,
		MetadataJSON: metadata,
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO pipeline_jobs (id, repository_id, phase, state, metadata_json)
		 VALUES (?, ?, ?, ?, ?)`,
		job.ID, repoID, phase, StatePending, metadata)
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

// RunSummary aggregates every phase belonging to one pipeline run
// (identified by run_id in pipeline_jobs.metadata_json). Used by
// `repowise pipeline status` + resume detection.
type RunSummary struct {
	RunID     string
	Phases    []Job // ordered by started_at
	StartedAt time.Time
	UpdatedAt time.Time
	// Overall is the run's verdict:
	//   "succeeded" — every phase completed
	//   "failed"    — at least one phase failed (terminal)
	//   "running"   — at least one phase is pending/running
	Overall string
}

// Outcome enumerates the RunSummary.Overall values.
const (
	OutcomeSucceeded = "succeeded"
	OutcomeFailed    = "failed"
	OutcomeRunning   = "running"
)

// LatestRun returns the most recent pipeline run for a repository, or
// (nil, nil) when none exist. A "run" is the set of pipeline_jobs rows
// sharing the same run_id in metadata_json; the latest run is the one
// whose newest row is most recent.
func (s *Store) LatestRun(ctx context.Context, repoID string) (*RunSummary, error) {
	if repoID == "" {
		return nil, fmt.Errorf("repoID is required")
	}
	// Pull every row with a non-empty run_id and group in Go — keeps the
	// query portable across SQLite/Postgres (json_extract has subtly
	// different syntax otherwise).
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, repository_id, phase, state, COALESCE(cursor,''), started_at, updated_at,
		        COALESCE(error,''), metadata_json
		 FROM pipeline_jobs WHERE repository_id = ?
		 ORDER BY started_at ASC, id ASC`,
		repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := map[string]*RunSummary{}
	order := []string{} // first-seen order of run_ids
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.RepositoryID, &j.Phase, &j.State, &j.Cursor,
			&j.StartedAt, &j.UpdatedAt, &j.Error, &j.MetadataJSON); err != nil {
			return nil, err
		}
		runID := extractRunID(j.MetadataJSON)
		if runID == "" {
			continue // legacy rows with no run_id, skip
		}
		r, ok := runs[runID]
		if !ok {
			r = &RunSummary{RunID: runID, StartedAt: j.StartedAt}
			runs[runID] = r
			order = append(order, runID)
		}
		r.Phases = append(r.Phases, j)
		if j.UpdatedAt.After(r.UpdatedAt) {
			r.UpdatedAt = j.UpdatedAt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	// Latest run = the one with the most recent UpdatedAt. Ties broken
	// by first-seen order (later runs are later in `order`).
	var latest *RunSummary
	for i := len(order) - 1; i >= 0; i-- {
		r := runs[order[i]]
		if latest == nil || r.UpdatedAt.After(latest.UpdatedAt) {
			latest = r
		}
	}
	latest.Overall = classifyRun(latest.Phases)
	return latest, nil
}

// classifyRun derives the overall verdict from the phase states. When
// a phase appears multiple times within the same run (resume attempts),
// only the LATEST row counts — a failure superseded by a success is
// not a failure. Earlier rows are already in `jobs` for the audit log,
// but they don't drive the overall outcome.
func classifyRun(jobs []Job) string {
	latestByPhase := map[string]Job{}
	for _, j := range jobs {
		existing, ok := latestByPhase[j.Phase]
		if !ok || j.UpdatedAt.After(existing.UpdatedAt) ||
			(j.UpdatedAt.Equal(existing.UpdatedAt) && j.ID > existing.ID) {
			latestByPhase[j.Phase] = j
		}
	}
	hasFailed := false
	hasInFlight := false
	for _, j := range latestByPhase {
		switch j.State {
		case StateFailed:
			hasFailed = true
		case StatePending, StateRunning:
			hasInFlight = true
		}
	}
	switch {
	case hasFailed:
		return OutcomeFailed
	case hasInFlight:
		return OutcomeRunning
	default:
		return OutcomeSucceeded
	}
}

// extractRunID pulls the run_id field out of a tiny metadata_json blob.
// Avoids importing encoding/json — the marshaled shape is a known
// `{"run_id":"<hex>"}` form. Returns "" when absent or malformed.
func extractRunID(meta string) string {
	const key = `"run_id":`
	i := strings.Index(meta, key)
	if i < 0 {
		return ""
	}
	rest := meta[i+len(key):]
	rest = strings.TrimLeft(rest, " \t")
	if !strings.HasPrefix(rest, `"`) {
		return ""
	}
	end := strings.IndexByte(rest[1:], '"')
	if end < 0 {
		return ""
	}
	return rest[1 : 1+end]
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
