// Package repos manages rows in the `repositories` table — vor's
// top-level container for a single indexed repository. Other persistence
// packages (graphstore, etc.) join via repository_id.
package repos

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Repository mirrors the `repositories` row. Timestamps stay zero on insert
// unless the caller supplies them — the SQL defaults handle that case.
type Repository struct {
	ID            string
	Name          string
	URL           string
	LocalPath     string
	DefaultBranch string
	HeadCommit    string
	SettingsJSON  string
	// Tracked marks a repo the serve daemon watches (toggled by
	// register/unregister). Ephemeral marks a disposable repo whose indexed
	// data is purged on unregister. TrackedAt is set when first tracked.
	Tracked   bool
	Ephemeral bool
	TrackedAt sql.NullTime
	CreatedAt time.Time
	UpdatedAt time.Time
}

// repoColumns is the canonical SELECT list, kept in one place so every
// query and scanRepo stay in lockstep.
const repoColumns = `id, name, url, local_path, default_branch, COALESCE(head_commit,''), settings_json, tracked, ephemeral, tracked_at, created_at, updated_at`

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(...any) error }

// scanRepo reads one row in repoColumns order.
func scanRepo(sc rowScanner) (*Repository, error) {
	r := &Repository{}
	if err := sc.Scan(&r.ID, &r.Name, &r.URL, &r.LocalPath, &r.DefaultBranch,
		&r.HeadCommit, &r.SettingsJSON, &r.Tracked, &r.Ephemeral, &r.TrackedAt,
		&r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	return r, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Store is the CRUD surface for repositories.
type Store struct {
	db *sql.DB
}

// New returns a Store bound to db. The caller owns the *sql.DB lifecycle.
func New(db *sql.DB) *Store { return &Store{db: db} }

// EnsureByLocalPath returns the existing repository row for localPath if one
// exists; otherwise it inserts a new row using `name` (defaults to the
// basename of localPath when empty) and returns it.
//
// This is the canonical entry point for "I want to index this repo, give me
// the row to attach data to" — used by ingest, init, update.
func (s *Store) EnsureByLocalPath(ctx context.Context, localPath, name string) (*Repository, error) {
	if localPath == "" {
		return nil, errors.New("localPath is required")
	}

	row := s.db.QueryRowContext(ctx,
		`SELECT `+repoColumns+` FROM repositories WHERE local_path = ?`, localPath)
	switch r, err := scanRepo(row); {
	case err == nil:
		return r, nil
	case errors.Is(err, sql.ErrNoRows):
		// fall through to insert
	default:
		return nil, fmt.Errorf("query repository by local_path: %w", err)
	}

	if name == "" {
		name = inferRepoName(localPath)
	}
	id := newID()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO repositories (id, name, url, local_path, default_branch, settings_json)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, name, "", localPath, "main", "{}"); err != nil {
		return nil, fmt.Errorf("insert repository: %w", err)
	}
	return s.EnsureByLocalPath(ctx, localPath, name)
}

// UpdateHeadCommit sets head_commit and bumps updated_at.
func (s *Store) UpdateHeadCommit(ctx context.Context, repoID, sha string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE repositories SET head_commit = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		sha, repoID)
	if err != nil {
		return fmt.Errorf("update head_commit: %w", err)
	}
	return nil
}

// Get returns the repository row with id, or nil + sql.ErrNoRows.
func (s *Store) Get(ctx context.Context, id string) (*Repository, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+repoColumns+` FROM repositories WHERE id = ?`, id)
	r, err := scanRepo(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("scan repository: %w", err)
	}
	return r, nil
}

// List returns every repository row, ordered by name.
func (s *Store) List(ctx context.Context) ([]Repository, error) {
	return s.queryRepos(ctx, `SELECT `+repoColumns+` FROM repositories ORDER BY name`)
}

// ListTracked returns only the repositories the daemon watches
// (tracked = 1), ordered by name.
func (s *Store) ListTracked(ctx context.Context) ([]Repository, error) {
	return s.queryRepos(ctx, `SELECT `+repoColumns+` FROM repositories WHERE tracked = 1 ORDER BY name`)
}

func (s *Store) queryRepos(ctx context.Context, query string, args ...any) ([]Repository, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list repositories: %w", err)
	}
	defer rows.Close()
	var out []Repository
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// SetTracked flips a repo's daemon-tracking state. tracked_at is stamped
// the first time it becomes tracked. ephemeral is recorded alongside so
// unregister knows whether to purge the repo's indexed data.
func (s *Store) SetTracked(ctx context.Context, id string, tracked, ephemeral bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE repositories
		    SET tracked = ?, ephemeral = ?,
		        tracked_at = CASE WHEN ? = 1 AND tracked_at IS NULL THEN CURRENT_TIMESTAMP ELSE tracked_at END,
		        updated_at = CURRENT_TIMESTAMP
		  WHERE id = ?`,
		b2i(tracked), b2i(ephemeral), b2i(tracked), id)
	if err != nil {
		return fmt.Errorf("set tracked: %w", err)
	}
	return nil
}

// Delete removes a repository row. CASCADE handles the dependent rows.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repositories WHERE id = ?`, id)
	return err
}

// inferRepoName takes the last path segment.
func inferRepoName(localPath string) string {
	trimmed := strings.TrimRight(localPath, "/")
	if i := strings.LastIndexByte(trimmed, '/'); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

// newID returns a uuid4 hex (32 chars, no dashes) — matches the Python
// ORM's `uuid4().hex` convention.
func newID() string {
	u := uuid.New()
	return strings.ReplaceAll(u.String(), "-", "")
}
