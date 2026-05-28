// Package parsestore is the per-file parse cache backing incremental
// indexing. Each row holds one file's serialised parse output keyed by
// content hash + parser version; on re-index the pipeline reuses rows whose
// hash and version still match, skipping the expensive tree-sitter parse.
package parsestore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Entry is one cached parse to upsert.
type Entry struct {
	Path        string
	ContentHash string
	ParsedJSON  []byte
}

// Cached is a row loaded from the cache.
type Cached struct {
	ContentHash string
	ParsedJSON  []byte
}

// Store reads and writes the parse cache.
type Store struct{ db *sql.DB }

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// LoadAll returns the cache for repoID as path -> Cached, but only rows whose
// parser_version matches the supplied version (others are treated as misses
// so a parser change transparently invalidates stale entries).
func (s *Store) LoadAll(ctx context.Context, repoID, parserVersion string) (map[string]Cached, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT file_path, content_hash, parsed_json FROM parse_cache
		 WHERE repository_id = ? AND parser_version = ?`, repoID, parserVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Cached{}
	for rows.Next() {
		var path, hash, parsed string
		if err := rows.Scan(&path, &hash, &parsed); err != nil {
			return nil, err
		}
		out[path] = Cached{ContentHash: hash, ParsedJSON: []byte(parsed)}
	}
	return out, rows.Err()
}

// Upsert writes/refreshes cache rows for repoID at the given parserVersion.
func (s *Store) Upsert(ctx context.Context, repoID, parserVersion string, entries []Entry) error {
	if len(entries) == 0 {
		return nil
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
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO parse_cache (
		id, repository_id, file_path, content_hash, parser_version, parsed_json
	) VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(repository_id, file_path) DO UPDATE SET
		content_hash = excluded.content_hash,
		parser_version = excluded.parser_version,
		parsed_json = excluded.parsed_json,
		updated_at = CURRENT_TIMESTAMP`)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.ExecContext(ctx,
			uuid.NewString(), repoID, e.Path, e.ContentHash, parserVersion, string(e.ParsedJSON),
		); err != nil {
			return fmt.Errorf("upsert %s: %w", e.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

// Prune deletes cache rows for repoID whose file_path is not in keep — i.e.
// files that no longer exist in the repo.
func (s *Store) Prune(ctx context.Context, repoID string, keep []string) (int, error) {
	present := make(map[string]struct{}, len(keep))
	for _, p := range keep {
		present[p] = struct{}{}
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT file_path FROM parse_cache WHERE repository_id = ?`, repoID)
	if err != nil {
		return 0, err
	}
	var stale []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, err
		}
		if _, ok := present[p]; !ok {
			stale = append(stale, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(stale) == 0 {
		return 0, nil
	}
	// Delete in chunks to keep the IN-list bounded.
	const chunk = 400
	deleted := 0
	for i := 0; i < len(stale); i += chunk {
		end := i + chunk
		if end > len(stale) {
			end = len(stale)
		}
		batch := stale[i:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch)+1)
		args = append(args, repoID)
		for _, p := range batch {
			args = append(args, p)
		}
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM parse_cache WHERE repository_id = ? AND file_path IN (`+placeholders+`)`, args...)
		if err != nil {
			return deleted, fmt.Errorf("prune: %w", err)
		}
		if n, err := res.RowsAffected(); err == nil {
			deleted += int(n)
		}
	}
	return deleted, nil
}

// Count returns the number of cached rows for repoID.
func (s *Store) Count(ctx context.Context, repoID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM parse_cache WHERE repository_id = ?`, repoID).Scan(&n)
	return n, err
}
