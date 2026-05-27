package runner_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/generation/runner"
	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/providers"
	_ "github.com/repowise-dev/repowise-go/internal/providers/mock"
)

// fixture builds a repo on disk + a DB seeded with graph_nodes for the
// supplied files. Returns repo root, repo ID, and the DB.
func fixture(t *testing.T, files map[string]string) (string, string, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	tmp := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
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
	r, _ := repos.New(conn).EnsureByLocalPath(ctx, tmp, "runner-test")

	// Seed graph_nodes for each file. Symbols are seeded as non-file
	// nodes so augmentSymbols picks them up.
	i := 0
	for rel := range files {
		i++
		_, err := conn.ExecContext(ctx, `
			INSERT INTO graph_nodes (id, repository_id, node_id, node_type, language, file_path)
			VALUES (?, ?, ?, 'file', 'Go', ?)`,
			fmt.Sprintf("n-%d", i), r.ID, rel, rel,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = conn.ExecContext(ctx, `
			INSERT INTO graph_nodes (id, repository_id, node_id, node_type, kind, name, file_path)
			VALUES (?, ?, ?, 'symbol', 'function', 'TopFunc', ?)`,
			fmt.Sprintf("s-%d", i), r.ID, rel+"::TopFunc", rel,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	return tmp, r.ID, conn
}

func mockProvider(t *testing.T) providers.Provider {
	t.Helper()
	p, err := providers.NewProvider("mock", providers.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRun_GeneratesPagesPerIndexedFile(t *testing.T) {
	root, repoID, conn := fixture(t, map[string]string{
		"a.go": "package a\nfunc TopFunc() {}\n",
		"b.go": "package b\nfunc TopFunc() {}\n",
	})
	summary, err := runner.Run(context.Background(), runner.Options{
		RepoRoot: root, RepositoryID: repoID, DB: conn,
		Provider: mockProvider(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.GeneratedCount != 2 {
		t.Errorf("GeneratedCount = %d, want 2", summary.GeneratedCount)
	}
	if summary.TotalOutputTokens == 0 {
		t.Errorf("expected nonzero output tokens")
	}
	// Each generated FileResult should have a Page populated.
	for _, f := range summary.Files {
		if f.Status != runner.StatusGenerated {
			t.Errorf("%s status = %s", f.Path, f.Status)
		}
		if f.Page == nil {
			t.Errorf("%s missing Page", f.Path)
		}
		if len(f.Page.Metadata) == 0 || f.Page.Metadata["stop_reason"] == "" {
			t.Errorf("%s missing metadata: %+v", f.Path, f.Page.Metadata)
		}
	}
}

func TestRun_SkipsFreshPages(t *testing.T) {
	root, repoID, conn := fixture(t, map[string]string{
		"a.go": "package a\n",
	})
	opts := runner.Options{
		RepoRoot: root, RepositoryID: repoID, DB: conn,
		Provider: mockProvider(t),
	}
	// First run generates.
	first, _ := runner.Run(context.Background(), opts)
	if first.GeneratedCount != 1 {
		t.Fatalf("first run: %d generated", first.GeneratedCount)
	}
	// Second run with unchanged source must skip.
	second, _ := runner.Run(context.Background(), opts)
	if second.SkippedCount != 1 {
		t.Errorf("second run: SkippedCount = %d, want 1", second.SkippedCount)
	}
	if second.GeneratedCount != 0 {
		t.Errorf("second run: GeneratedCount = %d, want 0", second.GeneratedCount)
	}
}

func TestRun_ForceRegeneratesEvenWhenFresh(t *testing.T) {
	root, repoID, conn := fixture(t, map[string]string{
		"a.go": "package a\n",
	})
	opts := runner.Options{
		RepoRoot: root, RepositoryID: repoID, DB: conn,
		Provider: mockProvider(t),
	}
	_, _ = runner.Run(context.Background(), opts)
	opts.Force = true
	summary, _ := runner.Run(context.Background(), opts)
	if summary.GeneratedCount != 1 {
		t.Errorf("--force: GeneratedCount = %d, want 1", summary.GeneratedCount)
	}
}

func TestRun_StaleSourceRegenerates(t *testing.T) {
	root, repoID, conn := fixture(t, map[string]string{
		"a.go": "package a\n",
	})
	opts := runner.Options{
		RepoRoot: root, RepositoryID: repoID, DB: conn,
		Provider: mockProvider(t),
	}
	_, _ = runner.Run(context.Background(), opts)
	// Mutate source so hash differs.
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\nvar X int\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	summary, _ := runner.Run(context.Background(), opts)
	if summary.GeneratedCount != 1 {
		t.Errorf("stale source: GeneratedCount = %d, want 1", summary.GeneratedCount)
	}
}

func TestRun_RespectsLimit(t *testing.T) {
	root, repoID, conn := fixture(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\n",
		"c.go": "package c\n",
	})
	summary, _ := runner.Run(context.Background(), runner.Options{
		RepoRoot: root, RepositoryID: repoID, DB: conn,
		Provider: mockProvider(t),
		Limit:    2,
	})
	if summary.GeneratedCount != 2 {
		t.Errorf("--limit 2: GeneratedCount = %d, want 2", summary.GeneratedCount)
	}
}

func TestRun_TargetFiltersToOneFile(t *testing.T) {
	root, repoID, conn := fixture(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\n",
	})
	summary, _ := runner.Run(context.Background(), runner.Options{
		RepoRoot: root, RepositoryID: repoID, DB: conn,
		Provider: mockProvider(t),
		Target:   "b.go",
	})
	if summary.GeneratedCount != 1 {
		t.Fatalf("--target b.go: GeneratedCount = %d, want 1", summary.GeneratedCount)
	}
	if summary.Files[0].Path != "b.go" {
		t.Errorf("--target b.go: generated %q", summary.Files[0].Path)
	}
}

func TestRun_MissingFileReportsMissing(t *testing.T) {
	root, repoID, conn := fixture(t, map[string]string{
		"a.go": "package a\n",
	})
	// Delete the file but leave the graph_node behind.
	_ = os.Remove(filepath.Join(root, "a.go"))
	summary, _ := runner.Run(context.Background(), runner.Options{
		RepoRoot: root, RepositoryID: repoID, DB: conn,
		Provider: mockProvider(t),
	})
	if summary.MissingCount != 1 {
		t.Errorf("MissingCount = %d, want 1", summary.MissingCount)
	}
	if summary.GeneratedCount != 0 {
		t.Errorf("GeneratedCount = %d, want 0", summary.GeneratedCount)
	}
}

func TestRun_DryRunDoesNotCallProvider(t *testing.T) {
	root, repoID, conn := fixture(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\n",
	})
	// Pass nil provider — dry-run path should still work.
	summary, err := runner.Run(context.Background(), runner.Options{
		RepoRoot: root, RepositoryID: repoID, DB: conn,
		DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.DryRunCount != 2 {
		t.Errorf("DryRunCount = %d, want 2", summary.DryRunCount)
	}
	if summary.GeneratedCount != 0 {
		t.Errorf("GeneratedCount = %d, want 0", summary.GeneratedCount)
	}
}

func TestRun_OnProgressCalledPerFile(t *testing.T) {
	root, repoID, conn := fixture(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\n",
	})
	var seen []string
	_, _ = runner.Run(context.Background(), runner.Options{
		RepoRoot: root, RepositoryID: repoID, DB: conn,
		Provider:   mockProvider(t),
		OnProgress: func(r runner.FileResult) { seen = append(seen, r.Path) },
	})
	if len(seen) != 2 {
		t.Errorf("OnProgress invocations = %d, want 2", len(seen))
	}
}

func TestRun_RejectsMissingOptions(t *testing.T) {
	cases := []runner.Options{
		{},
		{RepoRoot: "/x"},
		{RepoRoot: "/x", RepositoryID: "r"},
		{RepoRoot: "/x", RepositoryID: "r", DB: &sql.DB{}}, // provider missing, not dry-run
	}
	for i, opts := range cases {
		_, err := runner.Run(context.Background(), opts)
		if err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestRun_SymbolsAttachedToBundle(t *testing.T) {
	// Indirect: we can't see Bundle.Symbols from outside, but seeding a
	// symbol row should at minimum not break the run.
	root, repoID, conn := fixture(t, map[string]string{
		"a.go": "package a\nfunc TopFunc() {}\n",
	})
	summary, err := runner.Run(context.Background(), runner.Options{
		RepoRoot: root, RepositoryID: repoID, DB: conn,
		Provider: mockProvider(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.GeneratedCount != 1 {
		t.Errorf("symbol-bearing repo: GeneratedCount = %d", summary.GeneratedCount)
	}
}

func TestRun_SignalsBestEffort(t *testing.T) {
	// git_metadata + dead_code_findings + health_findings empty for this
	// repo — generation must still succeed.
	root, repoID, conn := fixture(t, map[string]string{"a.go": "package a\n"})
	summary, err := runner.Run(context.Background(), runner.Options{
		RepoRoot: root, RepositoryID: repoID, DB: conn,
		Provider: mockProvider(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.GeneratedCount != 1 {
		t.Errorf("no-signals repo: GeneratedCount = %d", summary.GeneratedCount)
	}
}
