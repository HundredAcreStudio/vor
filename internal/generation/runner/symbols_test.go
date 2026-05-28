package runner_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/generation/models"
	"github.com/HundredAcreStudio/vor/internal/generation/runner"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/persistence/wikistore"
	"github.com/HundredAcreStudio/vor/internal/providers"
	_ "github.com/HundredAcreStudio/vor/internal/providers/mock"
)

// symbolFixture seeds graph_nodes with one file + two symbol rows, plus
// one inbound edge so neighbor loading has something to find.
func symbolFixture(t *testing.T) (string, string, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	tmp := t.TempDir()
	src := `package x

func Caller() {
	Callee()
}

func Callee() {
	// body
}
`
	if err := os.WriteFile(filepath.Join(tmp, "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	conn, dialect, err := db.Open(ctx, db.OpenOptions{
		URL: "sqlite:" + filepath.Join(tmp, "wiki.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	r, _ := repos.New(conn).EnsureByLocalPath(ctx, tmp, "sym-test")

	// File node.
	exec := func(q string, args ...any) {
		if _, err := conn.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec: %v\n%s", err, q)
		}
	}
	exec(`INSERT INTO graph_nodes (id, repository_id, node_id, node_type, language, file_path)
	      VALUES (?, ?, 'x.go', 'file', 'Go', 'x.go')`, "n-file", r.ID)

	// Two symbol nodes.
	exec(`INSERT INTO graph_nodes (id, repository_id, node_id, node_type, kind, name, qualified_name, file_path, language, start_line, end_line)
	      VALUES (?, ?, 'x.go::Caller', 'symbol', 'function', 'Caller', 'x.Caller', 'x.go', 'Go', 3, 5)`, "n-caller", r.ID)
	exec(`INSERT INTO graph_nodes (id, repository_id, node_id, node_type, kind, name, qualified_name, file_path, language, start_line, end_line)
	      VALUES (?, ?, 'x.go::Callee', 'symbol', 'function', 'Callee', 'x.Callee', 'x.go', 'Go', 7, 9)`, "n-callee", r.ID)

	// Caller -> Callee edge.
	exec(`INSERT INTO graph_edges (id, repository_id, source_node_id, target_node_id, edge_type, confidence)
	      VALUES (?, ?, 'x.go::Caller', 'x.go::Callee', 'calls', 1.0)`, "e-1", r.ID)

	return tmp, r.ID, conn
}

func TestRun_SymbolDetails_PerSymbol(t *testing.T) {
	root, repoID, conn := symbolFixture(t)
	prov, _ := providers.NewProvider("mock", providers.Options{})

	summary, err := runner.Run(context.Background(), runner.Options{
		RepoRoot:     root,
		RepositoryID: repoID,
		DB:           conn,
		Provider:     prov,
		Kinds:        []models.PageKind{models.PageKindSymbolDetail},
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.GeneratedCount != 2 {
		t.Fatalf("GeneratedCount = %d, want 2: %+v", summary.GeneratedCount, summary.Files)
	}
	// Pages should be persisted keyed by symbol ID.
	store := wikistore.New(conn)
	caller, err := store.GetByTarget(context.Background(), repoID, models.PageKindSymbolDetail, "x.go::Caller")
	if err != nil {
		t.Fatal(err)
	}
	if caller.PageType != models.PageKindSymbolDetail {
		t.Errorf("PageType = %s", caller.PageType)
	}
	if caller.Metadata["symbol_kind"] != "function" {
		t.Errorf("symbol_kind metadata = %q", caller.Metadata["symbol_kind"])
	}
	if caller.Metadata["callee_count"] != "1" {
		t.Errorf("Caller callee_count = %q, want 1", caller.Metadata["callee_count"])
	}

	callee, _ := store.GetByTarget(context.Background(), repoID, models.PageKindSymbolDetail, "x.go::Callee")
	if callee.Metadata["caller_count"] != "1" {
		t.Errorf("Callee caller_count = %q, want 1", callee.Metadata["caller_count"])
	}
}

func TestRun_SymbolDetails_TargetFilter(t *testing.T) {
	root, repoID, conn := symbolFixture(t)
	prov, _ := providers.NewProvider("mock", providers.Options{})

	summary, _ := runner.Run(context.Background(), runner.Options{
		RepoRoot:     root,
		RepositoryID: repoID,
		DB:           conn,
		Provider:     prov,
		Kinds:        []models.PageKind{models.PageKindSymbolDetail},
		Target:       "x.go::Callee",
	})
	if summary.GeneratedCount != 1 {
		t.Errorf("--target Callee: GeneratedCount = %d, want 1", summary.GeneratedCount)
	}
	if len(summary.Files) != 1 || summary.Files[0].Path != "x.go::Callee" {
		t.Errorf("target filter ineffective: %+v", summary.Files)
	}
}

func TestRun_SymbolDetails_MissingHostFile(t *testing.T) {
	root, repoID, conn := symbolFixture(t)
	// Delete the file but leave graph_nodes pointing at it.
	if err := os.Remove(filepath.Join(root, "x.go")); err != nil {
		t.Fatal(err)
	}
	prov, _ := providers.NewProvider("mock", providers.Options{})

	summary, _ := runner.Run(context.Background(), runner.Options{
		RepoRoot:     root,
		RepositoryID: repoID,
		DB:           conn,
		Provider:     prov,
		Kinds:        []models.PageKind{models.PageKindSymbolDetail},
	})
	if summary.MissingCount != 2 {
		t.Errorf("MissingCount = %d, want 2 (both symbols)", summary.MissingCount)
	}
	if summary.GeneratedCount != 0 {
		t.Errorf("GeneratedCount = %d, want 0 when host file deleted", summary.GeneratedCount)
	}
}

func TestRun_SymbolDetails_DryRun(t *testing.T) {
	root, repoID, conn := symbolFixture(t)
	summary, err := runner.Run(context.Background(), runner.Options{
		RepoRoot:     root,
		RepositoryID: repoID,
		DB:           conn,
		Kinds:        []models.PageKind{models.PageKindSymbolDetail},
		DryRun:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.DryRunCount != 2 {
		t.Errorf("DryRunCount = %d, want 2", summary.DryRunCount)
	}
}

func TestRun_UnknownKindErrors(t *testing.T) {
	_, repoID, conn := symbolFixture(t)
	prov, _ := providers.NewProvider("mock", providers.Options{})
	_, err := runner.Run(context.Background(), runner.Options{
		RepoRoot:     "/tmp",
		RepositoryID: repoID,
		DB:           conn,
		Provider:     prov,
		Kinds:        []models.PageKind{"bogus"},
	})
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

// _ keeps the fmt import alive in case future test helpers need it.
var _ = fmt.Sprintf
