package commands

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/analysis/deadcode"
	"github.com/HundredAcreStudio/vor/internal/analysis/health"
	"github.com/HundredAcreStudio/vor/internal/ingestion/external"
	"github.com/HundredAcreStudio/vor/internal/ingestion/graph"
	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/deadstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/externalstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/graphstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/healthstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

// TestStatusCommand_RendersPersistedData uses the same shape Pass A
// ingest --persist produces: a real tmpdir repo with a populated wiki.db.
// The status command points at that repo and we assert the rendered text.
func TestStatusCommand_RendersPersistedData(t *testing.T) {
	tmp := t.TempDir()
	ctx := context.Background()

	// The global DB is resolved from VOR_DB_URL; point it at our seeded DB so
	// `vor status` reads the snapshot below.
	dbURL := "sqlite:" + filepath.Join(tmp, "vor.db")
	t.Setenv("VOR_DB_URL", dbURL)
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: dbURL})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	absTmp, _ := filepath.Abs(tmp)
	repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, absTmp, "test-repo")
	if err != nil {
		t.Fatalf("ensure repo: %v", err)
	}

	// Build + persist a tiny snapshot.
	g := graph.New()
	main := g.AddFileNode(models.FileInfo{Path: "main.go", Language: "go", IsEntryPoint: true})
	lib := g.AddFileNode(models.FileInfo{Path: "lib.go", Language: "go"})
	sym := g.AddSymbolNode("lib.go", models.Symbol{
		ID: "lib.go::Helper", Name: "Helper", Kind: models.KindFunction,
		Visibility: models.VisibilityPublic, Language: "go",
	})
	g.AddEdge(main, lib, models.EdgeImports, 1.0, nil)
	g.AddEdge(lib, sym, models.EdgeDefines, 1.0, nil)
	g.ComputeMetrics()
	if err := graphstore.New(conn).ReplaceGraph(ctx, repoRow.ID, g); err != nil {
		t.Fatalf("graph: %v", err)
	}
	if err := externalstore.New(conn).ReplaceAll(ctx, repoRow.ID, []external.Record{
		{Name: "react", Ecosystem: "npm", Category: "library", Version: "^18", DeclaredIn: "package.json"},
	}); err != nil {
		t.Fatalf("ext: %v", err)
	}
	if err := deadstore.New(conn).ReplaceAll(ctx, repoRow.ID, []deadcode.Finding{
		{Kind: deadcode.KindUnreachableFile, FilePath: "lonely.go", Confidence: 1.0, SafeToDelete: true},
	}); err != nil {
		t.Fatalf("dead: %v", err)
	}
	if err := healthstore.New(conn).ReplaceAll(ctx, repoRow.ID, health.Result{
		FileMetrics: []health.FileMetric{
			{FilePath: "main.go", Score: 9.5}, {FilePath: "lib.go", Score: 8.5},
		},
		Findings: []health.Finding{
			{FilePath: "main.go", BiomarkerType: health.BiomarkerHighComplexity,
				Severity: health.SeverityMedium},
		},
	}); err != nil {
		t.Fatalf("health: %v", err)
	}
	conn.Close()

	// Run the status command, capture stdout.
	cmd := Root()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"status", "--repo", tmp})
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("status execute: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"test-repo",
		"graph nodes",
		"3", // 2 files + 1 symbol
		"externals",
		"npm",
		"dead-code findings",
		"1",
		"code health",
		"9.00",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("status output missing %q\n--- got ---\n%s", want, got)
		}
	}
}
