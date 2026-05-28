// Package wikistore persists Page records to wiki_pages and maintains
// wiki_page_versions when a page is re-generated. One Page per
// (repository_id, page_type, target_path) tuple is "current"; previous
// generations are archived to wiki_page_versions on update.
//
// Indexing into page_fts (the FTS5 virtual table) is application-side:
// every insert/update mirrors the title + content into the index, and
// deletes drop the corresponding row.
package wikistore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/repowise-dev/repowise-go/internal/generation/models"
)

// Store wraps a *sql.DB with the wiki-page CRUD methods.
type Store struct {
	db *sql.DB
}

// New constructs a Store. The caller owns the DB lifecycle.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Upsert inserts a new page or replaces the existing one for the same
// (repository_id, page_type, target_path). On replace, the previous row
// is archived into wiki_page_versions with version=N-1.
func (s *Store) Upsert(ctx context.Context, p models.Page) (models.Page, error) {
	if p.RepositoryID == "" || p.PageType == "" || p.TargetPath == "" {
		return models.Page{}, errors.New("wikistore: RepositoryID, PageType, TargetPath required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.Page{}, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if p.Freshness == "" {
		p.Freshness = models.FreshnessFresh
	}
	if p.Version == 0 {
		p.Version = 1
	}

	existing, err := loadCurrent(ctx, tx, p.RepositoryID, p.PageType, p.TargetPath)
	if err != nil {
		return models.Page{}, err
	}
	if existing != nil {
		// Archive the existing row into wiki_page_versions, then update.
		if err := archive(ctx, tx, *existing); err != nil {
			return models.Page{}, fmt.Errorf("archive: %w", err)
		}
		p.ID = existing.ID
		p.CreatedAt = existing.CreatedAt
		p.Version = existing.Version + 1
		if err := updateRow(ctx, tx, p); err != nil {
			return models.Page{}, err
		}
	} else {
		p.ID = newID()
		if err := insertRow(ctx, tx, p); err != nil {
			return models.Page{}, err
		}
	}

	// Mirror into FTS5. The trigger on insert isn't set up — we maintain
	// it here so the same code path covers update + insert + delete.
	if _, err := tx.ExecContext(ctx, `DELETE FROM page_fts WHERE page_id = ?`, p.ID); err != nil {
		// If the FTS table doesn't exist (Postgres deployment), skip it.
		if !isMissingTable(err) {
			return models.Page{}, fmt.Errorf("fts delete: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO page_fts(page_id, title, content) VALUES (?, ?, ?)`,
		p.ID, p.Title, p.Content,
	); err != nil {
		if !isMissingTable(err) {
			return models.Page{}, fmt.Errorf("fts insert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return models.Page{}, err
	}
	return p, nil
}

// Get fetches a single page by id.
func (s *Store) Get(ctx context.Context, id string) (models.Page, error) {
	row := s.db.QueryRowContext(ctx, selectByID, id)
	return scanPage(row)
}

// ListByRepo returns every current page for a repo, ordered by target_path.
func (s *Store) ListByRepo(ctx context.Context, repoID string) ([]models.Page, error) {
	rows, err := s.db.QueryContext(ctx, selectByRepo, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Page{}
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetByTarget returns the current page for a (repo, kind, path) tuple,
// or sql.ErrNoRows if missing.
func (s *Store) GetByTarget(ctx context.Context, repoID string, kind models.PageKind, target string) (models.Page, error) {
	row := s.db.QueryRowContext(ctx, selectByTarget, repoID, string(kind), target)
	return scanPage(row)
}

// MarkStale flips freshness_status to "stale" for every page whose
// source_hash differs from the supplied value. Useful when the indexer
// has fresh source for a path and wants to invalidate stored pages.
func (s *Store) MarkStale(ctx context.Context, repoID, target string, freshHash string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE wiki_pages
		SET freshness_status = 'stale', updated_at = ?
		WHERE repository_id = ? AND target_path = ? AND source_hash != ?
	`, time.Now().UTC(), repoID, target, freshHash)
	return err
}

// ---- internal SQL --------------------------------------------------------

const baseColumns = `
	id, repository_id, page_type, title, content, summary, target_path,
	source_hash, model_name, provider_name,
	input_tokens, output_tokens, cached_tokens,
	generation_level, version, confidence, freshness_status,
	metadata_json, created_at, updated_at
`

var (
	selectByID     = "SELECT " + baseColumns + " FROM wiki_pages WHERE id = ?"
	selectByRepo   = "SELECT " + baseColumns + " FROM wiki_pages WHERE repository_id = ? ORDER BY target_path"
	selectByTarget = "SELECT " + baseColumns + " FROM wiki_pages WHERE repository_id = ? AND page_type = ? AND target_path = ?"
)

func loadCurrent(ctx context.Context, tx *sql.Tx, repoID string, kind models.PageKind, target string) (*models.Page, error) {
	row := tx.QueryRowContext(ctx, selectByTarget, repoID, string(kind), target)
	p, err := scanPage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func insertRow(ctx context.Context, tx *sql.Tx, p models.Page) error {
	meta := encodeMeta(p.Metadata)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO wiki_pages (
			id, repository_id, page_type, title, content, summary, target_path,
			source_hash, model_name, provider_name,
			input_tokens, output_tokens, cached_tokens,
			generation_level, version, confidence, freshness_status,
			metadata_json, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.RepositoryID, string(p.PageType), p.Title, p.Content, p.Summary, p.TargetPath,
		p.SourceHash, p.ModelName, p.ProviderName,
		p.InputTokens, p.OutputTokens, p.CachedTokens,
		p.GenLevel, p.Version, p.Confidence, string(p.Freshness),
		meta, p.CreatedAt, p.UpdatedAt,
	)
	return err
}

func updateRow(ctx context.Context, tx *sql.Tx, p models.Page) error {
	meta := encodeMeta(p.Metadata)
	_, err := tx.ExecContext(ctx, `
		UPDATE wiki_pages SET
			title=?, content=?, summary=?, source_hash=?, model_name=?,
			provider_name=?, input_tokens=?, output_tokens=?, cached_tokens=?,
			generation_level=?, version=?, confidence=?, freshness_status=?,
			metadata_json=?, updated_at=?
		WHERE id=?`,
		p.Title, p.Content, p.Summary, p.SourceHash, p.ModelName,
		p.ProviderName, p.InputTokens, p.OutputTokens, p.CachedTokens,
		p.GenLevel, p.Version, p.Confidence, string(p.Freshness),
		meta, p.UpdatedAt, p.ID,
	)
	return err
}

func archive(ctx context.Context, tx *sql.Tx, p models.Page) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO wiki_page_versions (
			id, page_id, repository_id, version, page_type, title, content,
			source_hash, model_name, provider_name,
			input_tokens, output_tokens, confidence, archived_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		newID(), p.ID, p.RepositoryID, p.Version, string(p.PageType),
		p.Title, p.Content, p.SourceHash, p.ModelName, p.ProviderName,
		p.InputTokens, p.OutputTokens, p.Confidence, time.Now().UTC(),
	)
	return err
}

// rowScanner is the common interface of sql.Row and sql.Rows for scanning.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanPage(r rowScanner) (models.Page, error) {
	var p models.Page
	var pageType, freshness, meta string
	if err := r.Scan(
		&p.ID, &p.RepositoryID, &pageType, &p.Title, &p.Content, &p.Summary, &p.TargetPath,
		&p.SourceHash, &p.ModelName, &p.ProviderName,
		&p.InputTokens, &p.OutputTokens, &p.CachedTokens,
		&p.GenLevel, &p.Version, &p.Confidence, &freshness,
		&meta, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return models.Page{}, err
	}
	p.PageType = models.PageKind(pageType)
	p.Freshness = models.FreshnessStatus(freshness)
	p.Metadata = decodeMeta(meta)
	return p, nil
}

func encodeMeta(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func decodeMeta(s string) map[string]string {
	if s == "" {
		return nil
	}
	out := map[string]string{}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// isMissingTable returns true when the error indicates the page_fts table
// doesn't exist — typically because we're on Postgres which doesn't have
// SQLite FTS5. Lets the caller silently skip FTS maintenance.
func isMissingTable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "does not exist")
}
