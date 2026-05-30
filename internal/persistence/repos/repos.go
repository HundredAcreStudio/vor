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

// Reinsert recreates a repositories row from a prior snapshot, preserving its
// id and tracked/ephemeral state. Used by `reindex` after a scorched-earth
// delete so the repo keeps its identity across the rebuild.
//
// This matters because repo-scoped settings (config health_rules, provider
// overrides, watch config) key on the repository id and have NO foreign key to
// repositories — they survive the cascade delete, so minting a fresh id would
// silently orphan them. Reusing the id keeps them attached. head_commit is
// left to the pipeline to re-set; created_at carries over so the repo's age is
// preserved.
func (s *Store) Reinsert(ctx context.Context, prior *Repository) (*Repository, error) {
	if prior == nil || prior.ID == "" || prior.LocalPath == "" {
		return nil, errors.New("prior repository with id and localPath is required")
	}
	name := prior.Name
	if name == "" {
		name = inferRepoName(prior.LocalPath)
	}
	branch := prior.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	settings := prior.SettingsJSON
	if settings == "" {
		settings = "{}"
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO repositories
		   (id, name, url, local_path, default_branch, settings_json, tracked, ephemeral, tracked_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		prior.ID, name, prior.URL, prior.LocalPath, branch, settings,
		b2i(prior.Tracked), b2i(prior.Ephemeral), prior.TrackedAt, prior.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("reinsert repository: %w", err)
	}
	return s.Get(ctx, prior.ID)
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

// GetByLocalPath returns the repository row for an exact local_path, or
// nil + sql.ErrNoRows. Read-only — unlike EnsureByLocalPath it never
// creates a row.
func (s *Store) GetByLocalPath(ctx context.Context, path string) (*Repository, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+repoColumns+` FROM repositories WHERE local_path = ?`, path)
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

// Slug converts a repository name into a URL-safe slug (lowercase, runs of
// non-alphanumerics collapsed to single hyphens, trimmed). Used for
// human-readable dashboard URLs (/repositories/<slug>/...).
func Slug(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if b.Len() > 0 && !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "repo"
	}
	return s
}

// UniqueSlugs returns id→slug for the given repositories, disambiguating
// collisions deterministically (the first repo by the slice's order keeps the
// clean slug; later collisions get a short id suffix). Callers pass the
// name-ordered List so the mapping is stable across calls.
func UniqueSlugs(rs []Repository) map[string]string {
	out := make(map[string]string, len(rs))
	taken := map[string]bool{}
	for _, r := range rs {
		s := Slug(r.Name)
		if taken[s] {
			s = s + "-" + shortID(r.ID)
		}
		taken[s] = true
		out[r.ID] = s
	}
	return out
}

func shortID(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}

// ResolveRef returns the repository identified by ref, which may be either a
// repository id or a slug (see UniqueSlugs). Returns sql.ErrNoRows when no
// repo matches.
func (s *Store) ResolveRef(ctx context.Context, ref string) (*Repository, error) {
	if r, err := s.Get(ctx, ref); err == nil {
		return r, nil // ref is a valid id
	}
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	slugs := UniqueSlugs(list)
	for i := range list {
		if slugs[list[i].ID] == ref {
			return &list[i], nil
		}
	}
	return nil, sql.ErrNoRows
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
