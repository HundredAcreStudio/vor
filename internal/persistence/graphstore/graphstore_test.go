package graphstore_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/ingestion/graph"
	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/graphstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
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

// makeRepo inserts a repository row and returns its ID.
func makeRepo(t *testing.T, conn *sql.DB) string {
	t.Helper()
	store := repos.New(conn)
	r, err := store.EnsureByLocalPath(context.Background(), "/tmp/gs-test", "gs-test")
	if err != nil {
		t.Fatalf("EnsureByLocalPath: %v", err)
	}
	return r.ID
}

// makeSampleGraph builds a small graph mirroring a tiny TS project:
//   - index.ts -> calc.ts (imports)
//   - index.ts::main calls calc.ts::add
//   - calc.ts defines Calculator class with one method add
func makeSampleGraph() *graph.Graph {
	g := graph.New()

	idx := g.AddFileNode(models.FileInfo{Path: "index.ts", Language: "typescript"})
	calc := g.AddFileNode(models.FileInfo{Path: "calc.ts", Language: "typescript"})

	parent := "Calculator"
	mainSym := g.AddSymbolNode("index.ts", models.Symbol{
		ID: "index.ts::main", Name: "main", Kind: models.KindFunction,
		Visibility: models.VisibilityPublic, Language: "typescript",
		StartLine: 1, EndLine: 5, QualifiedName: "main",
	})
	calcCls := g.AddSymbolNode("calc.ts", models.Symbol{
		ID: "calc.ts::Calculator", Name: "Calculator", Kind: models.KindClass,
		Visibility: models.VisibilityPublic, Language: "typescript",
		StartLine: 1, EndLine: 10, QualifiedName: "Calculator",
	})
	addSym := g.AddSymbolNode("calc.ts", models.Symbol{
		ID: "calc.ts::Calculator::add", Name: "add", Kind: models.KindMethod,
		ParentName: &parent, Visibility: models.VisibilityPublic, Language: "typescript",
		StartLine: 4, EndLine: 6, QualifiedName: "Calculator.add",
	})

	g.AddEdge(idx, mainSym, models.EdgeDefines, 1.0, nil)
	g.AddEdge(calc, calcCls, models.EdgeDefines, 1.0, nil)
	g.AddEdge(calc, addSym, models.EdgeDefines, 1.0, nil)
	g.AddEdge(calcCls, addSym, models.EdgeHasMethod, 1.0, nil)
	g.AddEdge(idx, calc, models.EdgeImports, 1.0, []string{"Calculator"})
	g.AddEdge(mainSym, addSym, models.EdgeCalls, 0.7, nil)

	g.ComputeMetrics()
	return g
}

func TestReplaceGraph_InsertsNodesAndEdges(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := graphstore.New(conn)
	ctx := context.Background()

	g := makeSampleGraph()
	if err := store.ReplaceGraph(ctx, repoID, g); err != nil {
		t.Fatalf("ReplaceGraph: %v", err)
	}

	nodes, err := store.CountNodes(ctx, repoID)
	if err != nil {
		t.Fatalf("CountNodes: %v", err)
	}
	if nodes != g.NodeCount() {
		t.Errorf("nodes persisted = %d, want %d", nodes, g.NodeCount())
	}

	edgeCounts, err := store.CountByEdgeType(ctx, repoID)
	if err != nil {
		t.Fatalf("CountByEdgeType: %v", err)
	}
	wantEdges := g.CountByEdgeType()
	for typ, want := range wantEdges {
		if got := edgeCounts[typ]; got != want {
			t.Errorf("edges[%s] = %d, want %d", typ, got, want)
		}
	}
}

func TestReplaceGraph_ReplacesOlderSnapshot(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := graphstore.New(conn)
	ctx := context.Background()

	g1 := makeSampleGraph()
	if err := store.ReplaceGraph(ctx, repoID, g1); err != nil {
		t.Fatalf("ReplaceGraph #1: %v", err)
	}

	// Build a smaller second graph (just one file, no edges).
	g2 := graph.New()
	g2.AddFileNode(models.FileInfo{Path: "only.go", Language: "go"})
	if err := store.ReplaceGraph(ctx, repoID, g2); err != nil {
		t.Fatalf("ReplaceGraph #2: %v", err)
	}

	nodes, _ := store.CountNodes(ctx, repoID)
	if nodes != 1 {
		t.Errorf("after replace: nodes = %d, want 1", nodes)
	}
	edges, _ := store.CountByEdgeType(ctx, repoID)
	if len(edges) != 0 {
		t.Errorf("after replace: edges = %v, want empty", edges)
	}
}

func TestReplaceGraph_CascadeOnRepoDelete(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := graphstore.New(conn)
	ctx := context.Background()

	if err := store.ReplaceGraph(ctx, repoID, makeSampleGraph()); err != nil {
		t.Fatalf("ReplaceGraph: %v", err)
	}

	// Delete the repo and verify rows cascade away — FK is ON DELETE CASCADE.
	if err := repos.New(conn).Delete(ctx, repoID); err != nil {
		t.Fatalf("Delete repo: %v", err)
	}
	nodes, _ := store.CountNodes(ctx, repoID)
	if nodes != 0 {
		t.Errorf("nodes after CASCADE delete = %d, want 0", nodes)
	}
	edges, _ := store.CountByEdgeType(ctx, repoID)
	if len(edges) != 0 {
		t.Errorf("edges after CASCADE delete = %v, want empty", edges)
	}
}

func TestReplaceGraph_PersistsSymbolFields(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := graphstore.New(conn)
	ctx := context.Background()

	if err := store.ReplaceGraph(ctx, repoID, makeSampleGraph()); err != nil {
		t.Fatalf("ReplaceGraph: %v", err)
	}

	// Spot-check one symbol row to confirm the per-symbol columns landed.
	var (
		kind, name, vis, parent string
		startLine, endLine      int
		pagerank                float64
	)
	row := conn.QueryRowContext(ctx,
		`SELECT kind, name, visibility, COALESCE(parent_symbol_id,''), start_line, end_line, pagerank
		 FROM graph_nodes WHERE repository_id = ? AND node_id = ?`,
		repoID, "calc.ts::Calculator::add")
	if err := row.Scan(&kind, &name, &vis, &parent, &startLine, &endLine, &pagerank); err != nil {
		t.Fatalf("query symbol row: %v", err)
	}
	if kind != "method" || name != "add" || vis != "public" || parent != "Calculator" {
		t.Errorf("symbol row mismatch: kind=%q name=%q vis=%q parent=%q",
			kind, name, vis, parent)
	}
	if startLine != 4 || endLine != 6 {
		t.Errorf("symbol lines = %d-%d, want 4-6", startLine, endLine)
	}
	if pagerank <= 0 {
		t.Errorf("pagerank = %v, want > 0 (ComputeMetrics ran)", pagerank)
	}
}
