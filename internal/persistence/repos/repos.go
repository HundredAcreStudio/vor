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
	CreatedAt     time.Time
	UpdatedAt     time.Time
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
		`SELECT id, name, url, local_path, default_branch, COALESCE(head_commit,''), settings_json, created_at, updated_at
		 FROM repositories WHERE local_path = ?`, localPath)
	r := &Repository{}
	switch err := row.Scan(&r.ID, &r.Name, &r.URL, &r.LocalPath, &r.DefaultBranch, &r.HeadCommit, &r.SettingsJSON, &r.CreatedAt, &r.UpdatedAt); {
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
		`SELECT id, name, url, local_path, default_branch, COALESCE(head_commit,''), settings_json, created_at, updated_at
		 FROM repositories WHERE id = ?`, id)
	r := &Repository{}
	if err := row.Scan(&r.ID, &r.Name, &r.URL, &r.LocalPath, &r.DefaultBranch, &r.HeadCommit, &r.SettingsJSON, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("scan repository: %w", err)
	}
	return r, nil
}

// List returns every repository row, ordered by name.
func (s *Store) List(ctx context.Context) ([]Repository, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, url, local_path, default_branch, COALESCE(head_commit,''), settings_json, created_at, updated_at
		 FROM repositories ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list repositories: %w", err)
	}
	defer rows.Close()
	var out []Repository
	for rows.Next() {
		var r Repository
		if err := rows.Scan(&r.ID, &r.Name, &r.URL, &r.LocalPath, &r.DefaultBranch, &r.HeadCommit, &r.SettingsJSON, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
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
