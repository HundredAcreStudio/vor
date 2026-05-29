package insights_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/ingestion/git"
	"github.com/HundredAcreStudio/vor/internal/ingestion/graph"
	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/insights"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/gitstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/graphstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

// fixture builds a tiny repo: main.go (hotspot, bus factor 1) imports lib.go.
func fixture(t *testing.T) (context.Context, *sql.DB, string) {
	t.Helper()
	ctx := context.Background()
	tmp := t.TempDir()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(tmp, "t.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	r, _ := repos.New(conn).EnsureByLocalPath(ctx, "/tmp/insights-test", "t")

	g := graph.New()
	main := g.AddFileNode(models.FileInfo{Path: "main.go", Language: "go", IsEntryPoint: true})
	lib := g.AddFileNode(models.FileInfo{Path: "lib.go", Language: "go"})
	g.AddEdge(main, lib, models.EdgeImports, 1.0, nil)
	g.ComputeMetrics()
	if err := graphstore.New(conn).ReplaceGraph(ctx, r.ID, g); err != nil {
		t.Fatal(err)
	}
	owner := git.AuthorShare{Name: "Alice", Email: "a@x", CommitCount: 9, CommitPct: 1.0}
	if err := gitstore.New(conn).ReplaceAll(ctx, r.ID, []git.PerFile{
		{Path: "main.go", CommitCountTotal: 9, CommitCount90d: 9, IsHotspot: true,
			ChurnPercentile: 0.95, PrimaryOwner: &owner, BusFactor: 1, ContributorCount: 1,
			TopAuthors: []git.AuthorShare{owner}},
	}); err != nil {
		t.Fatal(err)
	}
	return ctx, conn, r.ID
}

func TestRisk_FlagsHotspotSingleOwner(t *testing.T) {
	ctx, conn, repoID := fixture(t)
	out, err := insights.Risk(ctx, conn, repoID, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || !out[0].Hotspot || out[0].BusFactor != 1 || out[0].Risk != "high" {
		t.Fatalf("risk = %+v, want hotspot + busFactor 1 + high", out[0])
	}
}

func TestRisk_CountsDependents(t *testing.T) {
	ctx, conn, repoID := fixture(t)
	out, err := insights.Risk(ctx, conn, repoID, []string{"lib.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Dependents < 1 {
		t.Errorf("lib.go should have ≥1 dependent, got %+v", out)
	}
}

func TestAttention_HasKnowledgeSilo(t *testing.T) {
	ctx, conn, repoID := fixture(t)
	items, err := insights.Attention(ctx, conn, repoID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		if it.Category == "knowledge_silo" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a knowledge_silo item, got %+v", items)
	}
}

func TestGitInsights_BusFactorDistribution(t *testing.T) {
	ctx, conn, repoID := fixture(t)
	gi, err := insights.GitInsightsFor(ctx, conn, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if gi.BusFactor.Total != 1 || gi.BusFactor.Risk != 1 {
		t.Errorf("bus factor = %+v, want total 1 / risk 1", gi.BusFactor)
	}
}
