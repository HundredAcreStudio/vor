package parsestore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/parsestore"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

func setup(t *testing.T) (*parsestore.Store, string) {
	t.Helper()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(t.TempDir(), "wiki.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	r, err := repos.New(conn).EnsureByLocalPath(ctx, "/tmp/parse-test", "parse")
	if err != nil {
		t.Fatal(err)
	}
	return parsestore.New(conn), r.ID
}

func TestUpsertLoadPrune(t *testing.T) {
	ctx := context.Background()
	store, repoID := setup(t)

	entries := []parsestore.Entry{
		{Path: "a.go", ContentHash: "h1", ParsedJSON: []byte(`{"x":1}`)},
		{Path: "b.go", ContentHash: "h2", ParsedJSON: []byte(`{"x":2}`)},
	}
	if err := store.Upsert(ctx, repoID, "1", entries); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := store.LoadAll(ctx, repoID, "1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 || got["a.go"].ContentHash != "h1" {
		t.Errorf("load mismatch: %+v", got)
	}

	// A different parser version is a clean miss (stale entries ignored).
	if v2, _ := store.LoadAll(ctx, repoID, "2"); len(v2) != 0 {
		t.Errorf("version 2 should see no rows, got %d", len(v2))
	}

	// Upsert replaces in place (no duplicate rows).
	entries[0].ContentHash = "h1b"
	if err := store.Upsert(ctx, repoID, "1", entries[:1]); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = store.LoadAll(ctx, repoID, "1")
	if got["a.go"].ContentHash != "h1b" {
		t.Errorf("a.go hash = %q, want h1b after re-upsert", got["a.go"].ContentHash)
	}
	if n, _ := store.Count(ctx, repoID); n != 2 {
		t.Errorf("count = %d, want 2 (upsert, not insert)", n)
	}

	// Prune drops rows whose path is absent from keep.
	removed, err := store.Prune(ctx, repoID, []string{"a.go"})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed != 1 {
		t.Errorf("pruned = %d, want 1 (b.go)", removed)
	}
	if n, _ := store.Count(ctx, repoID); n != 1 {
		t.Errorf("count after prune = %d, want 1", n)
	}
}
