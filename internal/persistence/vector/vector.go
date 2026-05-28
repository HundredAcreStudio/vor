// Package vector persists embedding vectors and ranks them by cosine
// similarity. SQLite/Postgres have no native vector type in this port,
// so vectors are stored as little-endian float32 BLOBs and the
// nearest-neighbour search runs in Go: load the repo's candidate
// vectors, score each against the query, return the top-k. Repo-scale
// corpora (hundreds–thousands of embedded targets) rank in a few ms;
// swapping in pgvector later is a store-internal change.
package vector

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
)

// Kind classifies what an embedding points at.
const (
	KindPage   = "page"
	KindSymbol = "symbol"
)

// Record is one stored embedding.
type Record struct {
	RepositoryID string
	TargetKind   string
	TargetPath   string
	Model        string
	ContentHash  string
	Vector       []float32
}

// Match is one search hit.
type Match struct {
	TargetKind string  `json:"target_kind"`
	TargetPath string  `json:"target_path"`
	Score      float64 `json:"score"`
}

// Store reads/writes the embeddings table.
type Store struct{ db *sql.DB }

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Upsert writes (or replaces) the embedding for one target. The unique
// index on (repository_id, target_kind, target_path) makes this an
// idempotent re-embed.
func (s *Store) Upsert(ctx context.Context, r Record) error {
	if r.RepositoryID == "" || r.TargetPath == "" {
		return fmt.Errorf("RepositoryID and TargetPath required")
	}
	if len(r.Vector) == 0 {
		return fmt.Errorf("empty vector")
	}
	blob := encodeVector(r.Vector)
	// DELETE+INSERT keeps the path dialect-neutral (no ON CONFLICT
	// syntax divergence between sqlite and pg).
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM embeddings WHERE repository_id = ? AND target_kind = ? AND target_path = ?`,
		r.RepositoryID, r.TargetKind, r.TargetPath); err != nil {
		return fmt.Errorf("delete prior embedding: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO embeddings (id, repository_id, target_kind, target_path, model, dim, vector, content_hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		newID(), r.RepositoryID, r.TargetKind, r.TargetPath, r.Model, len(r.Vector), blob, r.ContentHash,
	); err != nil {
		return fmt.Errorf("insert embedding: %w", err)
	}
	return tx.Commit()
}

// ContentHashes returns target_path → content_hash for a repo, so a
// re-embed pass can skip targets whose source hasn't changed.
func (s *Store) ContentHashes(ctx context.Context, repoID, kind string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT target_path, content_hash FROM embeddings
		 WHERE repository_id = ? AND target_kind = ?`, repoID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, err
		}
		out[path] = hash
	}
	return out, rows.Err()
}

// Count returns how many embeddings exist for a repo.
func (s *Store) Count(ctx context.Context, repoID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM embeddings WHERE repository_id = ?`, repoID).Scan(&n)
	return n, err
}

// Search returns the top-k targets ranked by cosine similarity to
// query. kindFilter (KindPage/KindSymbol) narrows the candidate set;
// empty searches both. Loads candidate vectors of matching dimension
// and scores them in Go.
func (s *Store) Search(ctx context.Context, repoID string, query []float32, kindFilter string, k int) ([]Match, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("empty query vector")
	}
	if k <= 0 {
		k = 10
	}
	q := `SELECT target_kind, target_path, dim, vector FROM embeddings WHERE repository_id = ?`
	args := []any{repoID}
	if kindFilter != "" {
		q += " AND target_kind = ?"
		args = append(args, kindFilter)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	qnorm := norm(query)
	matches := []Match{}
	for rows.Next() {
		var kind, path string
		var dim int
		var blob []byte
		if err := rows.Scan(&kind, &path, &dim, &blob); err != nil {
			return nil, err
		}
		// Dimension mismatch (model changed) → skip rather than
		// score garbage.
		if dim != len(query) {
			continue
		}
		vec := decodeVector(blob, dim)
		matches = append(matches, Match{
			TargetKind: kind,
			TargetPath: path,
			Score:      cosine(query, qnorm, vec),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Score > matches[j].Score })
	if len(matches) > k {
		matches = matches[:k]
	}
	return matches, nil
}

// ---- vector math + (de)serialisation ------------------------------------

func cosine(a []float32, anorm float64, b []float32) float64 {
	bnorm := norm(b)
	if anorm == 0 || bnorm == 0 {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot / (anorm * bnorm)
}

func norm(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

// encodeVector packs float32s little-endian. 4 bytes per element.
func encodeVector(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeVector is the inverse of encodeVector. dim guards against a
// truncated blob.
func decodeVector(buf []byte, dim int) []float32 {
	if len(buf) < dim*4 {
		dim = len(buf) / 4
	}
	out := make([]float32, dim)
	for i := 0; i < dim; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
	}
	return out
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
