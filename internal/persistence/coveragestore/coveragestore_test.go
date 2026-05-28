package coveragestore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/health/coverage"
	"github.com/repowise-dev/repowise-go/internal/persistence/coveragestore"
	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

func setup(t *testing.T) (*coveragestore.Store, string) {
	t.Helper()
	ctx := context.Background()
	url := "sqlite:" + filepath.Join(t.TempDir(), "wiki.db")
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	r, err := repos.New(conn).EnsureByLocalPath(ctx, "/tmp/cov-test", "cov")
	if err != nil {
		t.Fatal(err)
	}
	return coveragestore.New(conn), r.ID
}

func TestUpsertAndMap(t *testing.T) {
	ctx := context.Background()
	store, repoID := setup(t)

	br := 80.0
	files := []coverage.FileCoverage{
		{Path: "a.go", Format: coverage.FormatLCOV, LinePct: 90, BranchPct: &br, CoveredLines: []int{1, 2, 3}, TotalCoverable: 3},
		{Path: "b.go", Format: coverage.FormatLCOV, LinePct: 30, CoveredLines: []int{1}, TotalCoverable: 3},
	}
	if err := store.Upsert(ctx, repoID, "deadbeef", files); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	m, err := store.CoverageMap(ctx, repoID)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if m["a.go"] != 90 || m["b.go"] != 30 {
		t.Errorf("coverage map = %v", m)
	}

	// Re-import with a changed number — upsert should replace, not duplicate.
	files[1].LinePct = 75
	if err := store.Upsert(ctx, repoID, "", files); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	n, _ := store.Count(ctx, repoID)
	if n != 2 {
		t.Errorf("count = %d, want 2 (upsert not insert)", n)
	}
	m, _ = store.CoverageMap(ctx, repoID)
	if m["b.go"] != 75 {
		t.Errorf("b.go coverage = %v, want 75 after re-import", m["b.go"])
	}
}
