package insights_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/ingestion/graph"
	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/insights"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/graphstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

func TestRescueMatch_StyleVariant(t *testing.T) {
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
	r, _ := repos.New(conn).EnsureByLocalPath(ctx, "/tmp/augment-test", "t")
	g := graph.New()
	g.AddFileNode(models.FileInfo{Path: "config/loader.go", Language: "go"})
	g.AddSymbolNode("config/loader.go", models.Symbol{
		ID: "config/loader.go::ParseYaml", Name: "ParseYaml", Kind: models.KindFunction, StartLine: 42, Language: "go",
	})
	g.ComputeMetrics()
	if err := graphstore.New(conn).ReplaceGraph(ctx, r.ID, g); err != nil {
		t.Fatal(err)
	}

	// snake_case grep miss should still rescue the PascalCase symbol.
	msg, ok, err := insights.RescueMatch(ctx, conn, r.ID, "parse_yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected a rescue match for parse_yaml")
	}
	if !strings.Contains(msg, "ParseYaml") || !strings.Contains(msg, "config/loader.go") {
		t.Errorf("rescue msg missing symbol/file: %q", msg)
	}

	// A pattern with no plausible match stays silent.
	if _, ok, _ := insights.RescueMatch(ctx, conn, r.ID, "zzqqxx"); ok {
		t.Errorf("expected no rescue for nonsense pattern")
	}
}

func TestTriageFiles_RanksByCentrality(t *testing.T) {
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
	r, _ := repos.New(conn).EnsureByLocalPath(ctx, "/tmp/augment-test", "t")
	g := graph.New()
	loader := g.AddFileNode(models.FileInfo{Path: "config/loader.go", Language: "go"})
	writer := g.AddFileNode(models.FileInfo{Path: "config/writer.go", Language: "go"})
	g.AddSymbolNode("config/loader.go", models.Symbol{ID: "config/loader.go::ConfigParse", Name: "ConfigParse", Kind: models.KindFunction, Language: "go"})
	g.AddEdge(writer, loader, models.EdgeImports, 1.0, nil) // loader is more central
	g.ComputeMetrics()
	if err := graphstore.New(conn).ReplaceGraph(ctx, r.ID, g); err != nil {
		t.Fatal(err)
	}

	files, err := insights.TriageFiles(ctx, conn, r.ID, "config", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("expected triage files for 'config'")
	}
	if files[0] != "config/loader.go" {
		t.Errorf("expected most-central file first, got %v", files)
	}
}
