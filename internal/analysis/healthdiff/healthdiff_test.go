package healthdiff_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/analysis/health"
	"github.com/HundredAcreStudio/vor/internal/analysis/healthdiff"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/healthstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/persistence/snapshotstore"
)

func setup(t *testing.T) (context.Context, *sql.DB, string) {
	t.Helper()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(t.TempDir(), "d.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	r, err := repos.New(conn).EnsureByLocalPath(ctx, t.TempDir(), "diff")
	if err != nil {
		t.Fatal(err)
	}
	return ctx, conn, r.ID
}

func TestCompute_NoBaseline(t *testing.T) {
	ctx, conn, repoID := setup(t)
	// Current health only, no snapshot.
	_ = healthstore.New(conn).ReplaceAll(ctx, repoID, health.Result{
		FileMetrics: []health.FileMetric{{FilePath: "a.go", Score: 8}},
	})
	d, err := healthdiff.Compute(ctx, conn, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if d.HasBaseline {
		t.Error("HasBaseline should be false with no snapshot")
	}
	if d.CurrentAvg != 8 {
		t.Errorf("CurrentAvg = %v, want 8", d.CurrentAvg)
	}
}

func TestCompute_RegressionsImprovementsNewRemoved(t *testing.T) {
	ctx, conn, repoID := setup(t)

	// Baseline snapshot at commit c1.
	if err := snapshotstore.New(conn).Insert(ctx, snapshotstore.Snapshot{
		RepositoryID:  repoID,
		CommitSHA:     "c1",
		Branch:        "main",
		AverageHealth: 6.67,
		PerFileScores: map[string]float64{"a.go": 8, "b.go": 5, "gone.go": 7},
	}); err != nil {
		t.Fatal(err)
	}

	// Current: a.go regressed 8→6, b.go improved 5→7, new.go added, gone.go removed.
	_ = healthstore.New(conn).ReplaceAll(ctx, repoID, health.Result{
		FileMetrics: []health.FileMetric{
			{FilePath: "a.go", Score: 6},
			{FilePath: "b.go", Score: 7},
			{FilePath: "new.go", Score: 9},
		},
	})

	d, err := healthdiff.Compute(ctx, conn, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if !d.HasBaseline || d.BaselineCommit != "c1" || d.BaselineBranch != "main" {
		t.Fatalf("baseline meta wrong: %+v", d)
	}
	if len(d.Regressions) != 1 || d.Regressions[0].Path != "a.go" || d.Regressions[0].Delta != -2 {
		t.Errorf("regressions = %+v, want [a.go -2]", d.Regressions)
	}
	if len(d.Improvements) != 1 || d.Improvements[0].Path != "b.go" || d.Improvements[0].Delta != 2 {
		t.Errorf("improvements = %+v, want [b.go +2]", d.Improvements)
	}
	if len(d.NewFiles) != 1 || d.NewFiles[0] != "new.go" {
		t.Errorf("newFiles = %v, want [new.go]", d.NewFiles)
	}
	if len(d.RemovedFiles) != 1 || d.RemovedFiles[0] != "gone.go" {
		t.Errorf("removedFiles = %v, want [gone.go]", d.RemovedFiles)
	}
}
