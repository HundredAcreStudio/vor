package snapshotstore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/persistence/snapshotstore"
)

func TestSnapshots_InsertLatestListPrune(t *testing.T) {
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(t.TempDir(), "s.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err) // also exercises the 0007 commit/branch columns
	}
	r, err := repos.New(conn).EnsureByLocalPath(ctx, t.TempDir(), "snap")
	if err != nil {
		t.Fatal(err)
	}
	s := snapshotstore.New(conn)

	// Empty repo → no latest.
	if latest, err := s.Latest(ctx, r.ID); err != nil || latest != nil {
		t.Fatalf("Latest on empty = %v, %v; want nil, nil", latest, err)
	}

	// Insert commit A.
	if err := s.Insert(ctx, snapshotstore.Snapshot{
		RepositoryID: r.ID, CommitSHA: "aaa", Branch: "main",
		AverageHealth: 8.0, HotspotHealth: 6.0, WorstPath: "x.go", WorstScore: 3.0,
		PerFileScores: map[string]float64{"x.go": 3.0, "y.go": 9.0},
	}); err != nil {
		t.Fatalf("Insert A: %v", err)
	}
	latest, err := s.Latest(ctx, r.ID)
	if err != nil || latest == nil || latest.CommitSHA != "aaa" {
		t.Fatalf("Latest after A = %+v, %v", latest, err)
	}
	if latest.Branch != "main" || latest.PerFileScores["x.go"] != 3.0 {
		t.Errorf("snapshot fields/per-file round-trip wrong: %+v", latest)
	}

	// Insert commit B → Latest is B; List chronological [A, B].
	if err := s.Insert(ctx, snapshotstore.Snapshot{RepositoryID: r.ID, CommitSHA: "bbb", AverageHealth: 9.0}); err != nil {
		t.Fatalf("Insert B: %v", err)
	}
	list, err := s.List(ctx, r.ID, 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %d snapshots, want 2 (%v)", len(list), err)
	}
	if list[0].CommitSHA != "aaa" || list[1].CommitSHA != "bbb" {
		t.Errorf("List not chronological: %q, %q", list[0].CommitSHA, list[1].CommitSHA)
	}

	// Prune to 1 keeps the newest.
	if err := s.Prune(ctx, r.ID, 1); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	list, _ = s.List(ctx, r.ID, 10)
	if len(list) != 1 || list[0].CommitSHA != "bbb" {
		t.Errorf("after prune = %+v, want [bbb]", list)
	}
}
