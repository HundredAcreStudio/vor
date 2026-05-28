package vector_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/persistence/vector"
)

func setup(t *testing.T) (*vector.Store, string) {
	t.Helper()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{
		URL: "sqlite:" + filepath.Join(t.TempDir(), "v.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r, _ := repos.New(conn).EnsureByLocalPath(ctx, t.TempDir(), "vec")
	return vector.New(conn), r.ID
}

func TestUpsertAndSearch_RanksByCosine(t *testing.T) {
	s, repo := setup(t)
	ctx := context.Background()

	// Three orthogonal-ish vectors. Query closest to "auth".
	must := func(kind, path string, v []float32) {
		if err := s.Upsert(ctx, vector.Record{
			RepositoryID: repo, TargetKind: kind, TargetPath: path,
			Model: "mock", Vector: v, ContentHash: "h",
		}); err != nil {
			t.Fatal(err)
		}
	}
	must(vector.KindPage, "auth.go", []float32{1, 0, 0})
	must(vector.KindPage, "db.go", []float32{0, 1, 0})
	must(vector.KindPage, "ui.go", []float32{0, 0, 1})

	matches, err := s.Search(ctx, repo, []float32{0.9, 0.1, 0}, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 3 {
		t.Fatalf("want 3 matches, got %d", len(matches))
	}
	if matches[0].TargetPath != "auth.go" {
		t.Errorf("top match = %q, want auth.go (closest cosine)", matches[0].TargetPath)
	}
	if matches[0].Score <= matches[1].Score {
		t.Errorf("scores not descending: %+v", matches)
	}
}

func TestSearch_KindFilter(t *testing.T) {
	s, repo := setup(t)
	ctx := context.Background()
	_ = s.Upsert(ctx, vector.Record{RepositoryID: repo, TargetKind: vector.KindPage, TargetPath: "p.go", Model: "m", Vector: []float32{1, 0}})
	_ = s.Upsert(ctx, vector.Record{RepositoryID: repo, TargetKind: vector.KindSymbol, TargetPath: "p.go::F", Model: "m", Vector: []float32{1, 0}})

	pages, _ := s.Search(ctx, repo, []float32{1, 0}, vector.KindPage, 10)
	if len(pages) != 1 || pages[0].TargetKind != vector.KindPage {
		t.Errorf("kind filter 'page' returned %+v", pages)
	}
	syms, _ := s.Search(ctx, repo, []float32{1, 0}, vector.KindSymbol, 10)
	if len(syms) != 1 || syms[0].TargetKind != vector.KindSymbol {
		t.Errorf("kind filter 'symbol' returned %+v", syms)
	}
}

func TestUpsert_Idempotent(t *testing.T) {
	s, repo := setup(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := s.Upsert(ctx, vector.Record{
			RepositoryID: repo, TargetKind: vector.KindPage, TargetPath: "x.go",
			Model: "m", Vector: []float32{1, 2, 3}, ContentHash: "h1",
		}); err != nil {
			t.Fatal(err)
		}
	}
	n, _ := s.Count(ctx, repo)
	if n != 1 {
		t.Errorf("re-upsert should not duplicate: count = %d, want 1", n)
	}
}

func TestSearch_DimensionMismatchSkipped(t *testing.T) {
	s, repo := setup(t)
	ctx := context.Background()
	_ = s.Upsert(ctx, vector.Record{RepositoryID: repo, TargetKind: vector.KindPage, TargetPath: "old.go", Model: "old", Vector: []float32{1, 2, 3, 4}})
	_ = s.Upsert(ctx, vector.Record{RepositoryID: repo, TargetKind: vector.KindPage, TargetPath: "new.go", Model: "new", Vector: []float32{1, 0}})

	// Query is 2-dim — the 4-dim "old.go" row must be skipped, not crash.
	matches, err := s.Search(ctx, repo, []float32{1, 0}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].TargetPath != "new.go" {
		t.Errorf("dimension mismatch not skipped: %+v", matches)
	}
}

func TestContentHashes_RoundTrip(t *testing.T) {
	s, repo := setup(t)
	ctx := context.Background()
	_ = s.Upsert(ctx, vector.Record{RepositoryID: repo, TargetKind: vector.KindPage, TargetPath: "a.go", Model: "m", Vector: []float32{1}, ContentHash: "hashA"})
	_ = s.Upsert(ctx, vector.Record{RepositoryID: repo, TargetKind: vector.KindPage, TargetPath: "b.go", Model: "m", Vector: []float32{1}, ContentHash: "hashB"})

	hashes, err := s.ContentHashes(ctx, repo, vector.KindPage)
	if err != nil {
		t.Fatal(err)
	}
	if hashes["a.go"] != "hashA" || hashes["b.go"] != "hashB" {
		t.Errorf("content hashes round-trip: %+v", hashes)
	}
}

func TestUpsert_RejectsEmpty(t *testing.T) {
	s, repo := setup(t)
	ctx := context.Background()
	if err := s.Upsert(ctx, vector.Record{RepositoryID: repo, TargetPath: "x", TargetKind: "page"}); err == nil {
		t.Error("expected error for empty vector")
	}
	if err := s.Upsert(ctx, vector.Record{TargetPath: "x", Vector: []float32{1}}); err == nil {
		t.Error("expected error for empty repo id")
	}
}
