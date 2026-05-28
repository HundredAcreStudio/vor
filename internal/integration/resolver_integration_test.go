// Package integration runs end-to-end tests of the ingest pipeline
// against language-specific fixture repositories. Each test:
//
//  1. Opens a fresh SQLite DB in t.TempDir() so persistence is isolated.
//  2. Points pipeline.Run() at testdata/fixtures/<lang>-project.
//  3. Asserts the resulting graph has the import edges the language
//     resolver should have produced — proving the modular resolver
//     registry handles each language end-to-end.
//
// Add a new language to the matrix when its resolver lands.
package integration

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/graphstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/pipeline"

	// Resolvers + parsers + manifest extractors. Each test exercises
	// the full pipeline so we need all registries populated.
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/cargo"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/gomod"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/npm"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/nuget"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/pypi"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/cpp"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/csharp"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/golang"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/java"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/javascript"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/python"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/rust"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/typescript"
)

// fixturePath returns the absolute path to a fixture directory by name.
// Resolves via filepath.Abs so the integration test works regardless of
// where `go test` is invoked from.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs("../../testdata/fixtures/" + name)
	if err != nil {
		t.Fatalf("resolve fixture: %v", err)
	}
	return abs
}

// runPipeline initialises a fresh DB, ensures a repository row for the
// fixture, runs pipeline.Run, and returns the opened DB + repo ID. The
// caller can then SQL-query the persisted graph_edges to verify
// resolver behaviour.
func runPipeline(t *testing.T, fixtureName string) (*sql.DB, string) {
	t.Helper()
	ctx := context.Background()
	tmp := t.TempDir()
	dbURL := "sqlite:" + filepath.Join(tmp, "wiki.db")

	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: dbURL})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	repoPath := fixturePath(t, fixtureName)
	repo, err := repos.New(conn).EnsureByLocalPath(ctx, repoPath, fixtureName)
	if err != nil {
		t.Fatalf("ensure repo: %v", err)
	}

	if _, err := pipeline.Run(ctx, pipeline.Options{
		RepoPath:     repoPath,
		Mode:         pipeline.ModeInit,
		DB:           conn,
		RepositoryID: repo.ID,
	}); err != nil {
		t.Fatalf("pipeline.Run: %v", err)
	}
	return conn, repo.ID
}

// edgesFrom returns the target paths of `imports` edges originating at
// source — what files does `source` import?
func edgesFrom(t *testing.T, conn *sql.DB, repoID, source string) []string {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(),
		`SELECT target_node_id FROM graph_edges
		 WHERE repository_id = ? AND source_node_id = ? AND edge_type = 'imports'
		 ORDER BY target_node_id`,
		repoID, source)
	if err != nil {
		t.Fatalf("query edges: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// assertImports checks that source's imports include every want entry.
// Extra edges are allowed — the resolver is permitted to be more
// thorough than the test asserts.
func assertImports(t *testing.T, conn *sql.DB, repoID, source string, want ...string) {
	t.Helper()
	got := edgesFrom(t, conn, repoID, source)
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s should import %s; got %v", source, w, got)
		}
	}
}

// ---- Rust -----------------------------------------------------------------

func TestRustProject_FullPipeline(t *testing.T) {
	conn, repoID := runPipeline(t, "rust-project")

	// main.rs imports calc and greeter via the crate-name prefix.
	assertImports(t, conn, repoID, "src/main.rs",
		"src/calc.rs",
		"src/greeter.rs",
	)

	// Verify external-systems extraction picked up the Cargo.toml dep.
	var serdeCount int
	if err := conn.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM external_systems
		 WHERE repository_id = ? AND name = 'serde'`,
		repoID).Scan(&serdeCount); err != nil {
		t.Fatalf("query serde: %v", err)
	}
	if serdeCount != 1 {
		t.Errorf("expected one serde dep, got %d", serdeCount)
	}

	// unused.rs has no incoming edges → dead-code analyzer should flag it.
	deadCount := countDeadCodeFor(t, conn, repoID, "src/unused.rs", true)
	if deadCount == 0 {
		t.Errorf("src/unused.rs should be flagged dead-code (it's not declared in lib.rs)")
	}
}

// ---- Java -----------------------------------------------------------------

func TestJavaProject_FullPipeline(t *testing.T) {
	conn, repoID := runPipeline(t, "java-project")

	assertImports(t, conn, repoID, "src/main/java/com/example/Main.java",
		"src/main/java/com/example/util/Greeter.java",
		"src/main/java/com/example/util/Calculator.java",
	)

	// Unused.java is never imported → dead-code candidate.
	if countDeadCodeFor(t, conn, repoID, "src/main/java/com/example/util/Unused.java", false) == 0 {
		t.Errorf("Unused.java should be flagged dead-code")
	}
}

// ---- C# -------------------------------------------------------------------

func TestCSharpProject_FullPipeline(t *testing.T) {
	conn, repoID := runPipeline(t, "csharp-project")

	// `using Demo.Util` resolves to every .cs file in Demo/Util/ — both
	// Calculator.cs and Status.cs since they share the namespace.
	got := edgesFrom(t, conn, repoID, "Demo/Program.cs")
	for _, want := range []string{"Demo/Util/Calculator.cs", "Demo/Util/Status.cs"} {
		if !slices.Contains(got, want) {
			t.Errorf("Program.cs should import %s; got %v", want, got)
		}
	}
}

// ---- C/C++ ----------------------------------------------------------------

func TestCppProject_FullPipeline(t *testing.T) {
	conn, repoID := runPipeline(t, "cpp-project")

	// main.cpp #includes the two headers. Both calc.h and greeter.h
	// live under include/ — the resolver finds them via the default
	// "include" CppIncludeDirs entry.
	assertImports(t, conn, repoID, "src/main.cpp",
		"include/calc.h",
		"include/greeter.h",
	)

	// calc.cpp includes calc.h.
	assertImports(t, conn, repoID, "src/calc.cpp", "include/calc.h")
}

// ---- helpers --------------------------------------------------------------

// countDeadCodeFor counts dead_code_findings rows matching path. When
// safeOnly is true, only SafeToDelete rows count.
func countDeadCodeFor(t *testing.T, conn *sql.DB, repoID, path string, safeOnly bool) int {
	t.Helper()
	query := `SELECT COUNT(*) FROM dead_code_findings
	          WHERE repository_id = ? AND file_path = ?`
	args := []any{repoID, path}
	if safeOnly {
		query += " AND safe_to_delete = 1"
	}
	var n int
	if err := conn.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count dead-code: %v", err)
	}
	return n
}

// statSnapshot is a tiny sanity-check helper used to confirm fixture
// repositories are non-empty.
//
//nolint:unused // kept for future fixture additions
func statSnapshot(t *testing.T, conn *sql.DB, repoID string) (nodes, edges, externals int) {
	t.Helper()
	ctx := context.Background()
	_ = conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_nodes WHERE repository_id = ?`, repoID).Scan(&nodes)
	_ = conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_edges WHERE repository_id = ?`, repoID).Scan(&edges)
	_ = conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM external_systems WHERE repository_id = ?`, repoID).Scan(&externals)
	return
}

// keep models import for future test cases referencing model types.
var _ = models.EdgeImports
var _ = graphstore.New
