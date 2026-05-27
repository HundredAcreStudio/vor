package healthstore_test

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/health"
	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/healthstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	tmp := t.TempDir()
	url := "sqlite:" + filepath.Join(tmp, "wiki.db")
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: url})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return conn
}

func makeRepo(t *testing.T, conn *sql.DB) string {
	t.Helper()
	r, err := repos.New(conn).EnsureByLocalPath(context.Background(), "/r", "r")
	if err != nil {
		t.Fatalf("ensure repo: %v", err)
	}
	return r.ID
}

func TestReplaceAll_PersistsAggregatesAndFindings(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := healthstore.New(conn)
	ctx := context.Background()

	result := health.Result{
		Findings: []health.Finding{
			{FilePath: "a.go", BiomarkerType: health.BiomarkerHighComplexity,
				Severity: health.SeverityHigh, FunctionName: "Big", LineStart: 1, LineEnd: 50,
				HealthImpact: 3, Reason: "ccn = 25",
				Details: map[string]any{"complexity": 25}},
			{FilePath: "a.go", BiomarkerType: health.BiomarkerLongFunction,
				Severity: health.SeverityMedium, FunctionName: "Big", LineStart: 1, LineEnd: 50,
				HealthImpact: 1, Reason: "function length = 50",
				Details: map[string]any{"lines": 50}},
			{FilePath: "b.go", BiomarkerType: health.BiomarkerHighComplexity,
				Severity: health.SeverityMedium, FunctionName: "Mid", LineStart: 1, LineEnd: 10,
				HealthImpact: 1, Reason: "ccn = 12"},
		},
		FileMetrics: []health.FileMetric{
			{FilePath: "a.go", Score: 6.0, MaxCCN: 25, NLOC: 50},
			{FilePath: "b.go", Score: 9.0, MaxCCN: 12, NLOC: 10},
			{FilePath: "c.go", Score: 10.0, MaxCCN: 3, NLOC: 30},
		},
	}
	if err := store.ReplaceAll(ctx, repoID, result); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}

	if n, _ := store.CountFindings(ctx, repoID); n != 3 {
		t.Errorf("CountFindings = %d, want 3", n)
	}

	byKind, err := store.CountByBiomarker(ctx, repoID)
	if err != nil {
		t.Fatalf("CountByBiomarker: %v", err)
	}
	if byKind[health.BiomarkerHighComplexity] != 2 || byKind[health.BiomarkerLongFunction] != 1 {
		t.Errorf("CountByBiomarker = %v", byKind)
	}

	avg, err := store.AverageScore(ctx, repoID)
	if err != nil {
		t.Fatalf("AverageScore: %v", err)
	}
	want := (6.0 + 9.0 + 10.0) / 3.0
	if math.Abs(avg-want) > 1e-9 {
		t.Errorf("AverageScore = %v, want %v", avg, want)
	}
}

func TestReplaceAll_OverwritesPreviousSnapshot(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := healthstore.New(conn)
	ctx := context.Background()

	_ = store.ReplaceAll(ctx, repoID, health.Result{
		Findings: []health.Finding{
			{FilePath: "x.go", BiomarkerType: health.BiomarkerHighComplexity,
				Severity: health.SeverityMedium, Reason: "old"},
		},
	})
	_ = store.ReplaceAll(ctx, repoID, health.Result{
		Findings: []health.Finding{
			{FilePath: "y.go", BiomarkerType: health.BiomarkerHighComplexity,
				Severity: health.SeverityMedium, Reason: "new"},
		},
	})
	if n, _ := store.CountFindings(ctx, repoID); n != 1 {
		t.Errorf("CountFindings after replace = %d, want 1", n)
	}
}

func TestAverageScore_EmptyRepoReturnsPerfect(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	avg, err := healthstore.New(conn).AverageScore(context.Background(), repoID)
	if err != nil {
		t.Fatalf("AverageScore: %v", err)
	}
	if avg != 10.0 {
		t.Errorf("empty repo AverageScore = %v, want 10.0", avg)
	}
}
