// Package coststore writes per-call LLM cost rows into the llm_costs
// table. Used by the provider wrapper layer to record spend as it
// happens.
package coststore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/repowise-dev/repowise-go/internal/providers"
)

// Entry is one persisted llm_costs row. Persistence is fire-and-forget —
// callers don't usually wait on errors here, but Insert returns them for
// tests and explicit accounting flows.
type Entry struct {
	RepositoryID string
	Timestamp    time.Time // zero = use DB default
	Model        string
	Operation    string
	Usage        providers.Usage
	CostUSD      float64
	FilePath     string // optional
}

// Store inserts and aggregates llm_costs rows.
type Store struct{ db *sql.DB }

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Insert writes one cost entry. RepositoryID must be set.
func (s *Store) Insert(ctx context.Context, e Entry) error {
	if e.RepositoryID == "" {
		return fmt.Errorf("RepositoryID is required")
	}
	var ts interface{}
	if !e.Timestamp.IsZero() {
		ts = e.Timestamp
	}
	var filePath interface{}
	if e.FilePath != "" {
		filePath = e.FilePath
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO llm_costs (
		repository_id, ts, model, operation,
		input_tokens, output_tokens, cost_usd, file_path
	) VALUES (?, COALESCE(?, CURRENT_TIMESTAMP), ?, ?, ?, ?, ?, ?)`,
		e.RepositoryID, ts, e.Model, e.Operation,
		e.Usage.InputTokens, e.Usage.OutputTokens, e.CostUSD, filePath)
	if err != nil {
		return fmt.Errorf("insert llm_costs: %w", err)
	}
	return nil
}

// TotalUSD returns the total spend for a repository. Zero when no rows.
func (s *Store) TotalUSD(ctx context.Context, repoID string) (float64, error) {
	var total sql.NullFloat64
	err := s.db.QueryRowContext(ctx,
		`SELECT SUM(cost_usd) FROM llm_costs WHERE repository_id = ?`, repoID).Scan(&total)
	if err != nil {
		return 0, err
	}
	if !total.Valid {
		return 0, nil
	}
	return total.Float64, nil
}

// TotalByOperation returns the per-operation spend for a repository.
// Returns map[operation] = USD.
func (s *Store) TotalByOperation(ctx context.Context, repoID string) (map[string]float64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT operation, SUM(cost_usd) FROM llm_costs WHERE repository_id = ? GROUP BY operation`,
		repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var op string
		var sum float64
		if err := rows.Scan(&op, &sum); err != nil {
			return nil, err
		}
		out[op] = sum
	}
	return out, rows.Err()
}

// Count returns the row count for a repository.
func (s *Store) Count(ctx context.Context, repoID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM llm_costs WHERE repository_id = ?`, repoID).Scan(&n)
	return n, err
}
