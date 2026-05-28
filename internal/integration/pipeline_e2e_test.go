package integration

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/pipelinestore"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/pipeline"

	// Decision extractors — the e2e run exercises the decisions phase
	// too, so register them alongside the parser/resolver imports the
	// package already pulls in (resolver_integration_test.go).
	_ "github.com/repowise-dev/repowise-go/internal/analysis/decisions/adr"
	_ "github.com/repowise-dev/repowise-go/internal/analysis/decisions/changelog"
	_ "github.com/repowise-dev/repowise-go/internal/analysis/decisions/commits"
	_ "github.com/repowise-dev/repowise-go/internal/analysis/decisions/inline"
)

// TestSampleRepo_FullPipeline is the end-to-end regression lock for the
// whole pipeline. It runs every phase against testdata/sample-repo — a
// deliberately multi-language fixture (Go + Python + TypeScript, with
// npm / pypi / go.mod manifests plus .gitignore + .repowiseIgnore
// exclusion cases) — and asserts the persisted state has the expected
// shape. Concrete counts that depend on parser internals are checked as
// ">0" lower bounds; the structural invariants (which phases run, which
// ecosystems get detected, exclusions honoured) are checked exactly.
//
// The sample repo has no .git, so the git phase degrades to a no-op —
// that's an asserted invariant, not an accident.
func TestSampleRepo_FullPipeline(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(tmp, "wiki.db")})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	repoPath, err := filepath.Abs("../../testdata/sample-repo")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := repos.New(conn).EnsureByLocalPath(ctx, repoPath, "sample-repo")
	if err != nil {
		t.Fatalf("ensure repo: %v", err)
	}

	res, err := pipeline.Run(ctx, pipeline.Options{
		RepoPath:     repoPath,
		Mode:         pipeline.ModeInit,
		DB:           conn,
		RepositoryID: repo.ID,
	})
	if err != nil {
		t.Fatalf("pipeline.Run: %v", err)
	}

	// --- phase invariants -------------------------------------------------
	if len(res.Phases) != 9 {
		t.Errorf("phases ran = %d, want 9", len(res.Phases))
	}
	for _, p := range res.Phases {
		if p.State != pipelinestore.StateCompleted {
			t.Errorf("phase %s = %s, want completed", p.Phase, p.State)
		}
	}
	// run_id grouping: LatestRun should see a single succeeded run.
	latest, err := pipelinestore.New(conn).LatestRun(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.Overall != pipelinestore.OutcomeSucceeded {
		t.Errorf("LatestRun = %+v, want a succeeded run", latest)
	}

	// --- parsed-files invariants ------------------------------------------
	// The fixture has Go, Python, and TypeScript sources that should
	// parse; exclusions should keep the noise out.
	langs := map[string]bool{}
	parsedPaths := map[string]bool{}
	for _, pf := range res.Parsed {
		langs[string(pf.FileInfo.Language)] = true
		parsedPaths[filepath.ToSlash(pf.FileInfo.Path)] = true
	}
	for _, want := range []string{"go", "python", "typescript"} {
		if !langs[want] {
			t.Errorf("expected %s files parsed, got languages %v", want, keys(langs))
		}
	}
	// Exclusions: .gitignore, .repowiseIgnore, node_modules, build/,
	// and binary/minified files must NOT appear among parsed files.
	for _, excluded := range []string{
		"ignored_by_gitignore.py",
		"ignored_by_repowise.py",
		"node_modules/lib/junk.js",
		"build/built.js",
		"bundle.min.js",
		"pkg/generated.go",
	} {
		if parsedPaths[excluded] {
			t.Errorf("%s should have been excluded from parsing", excluded)
		}
	}

	// --- graph persisted --------------------------------------------------
	if res.Graph == nil || res.Graph.NodeCount() == 0 {
		t.Fatal("graph empty after pipeline")
	}
	var nodeRows int
	_ = conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_nodes WHERE repository_id = ?`, repo.ID).Scan(&nodeRows)
	if nodeRows == 0 {
		t.Error("graph_nodes not persisted")
	}

	// --- externals: npm + pypi + go all detected --------------------------
	ecos := map[string]int{}
	rows, err := conn.QueryContext(ctx,
		`SELECT ecosystem, COUNT(*) FROM external_systems WHERE repository_id = ? GROUP BY ecosystem`,
		repo.ID)
	if err != nil {
		t.Fatalf("query externals: %v", err)
	}
	for rows.Next() {
		var eco string
		var n int
		if err := rows.Scan(&eco, &n); err != nil {
			t.Fatal(err)
		}
		ecos[eco] = n
	}
	rows.Close()
	for _, want := range []string{"npm", "pypi", "go"} {
		if ecos[want] == 0 {
			t.Errorf("expected %s externals, got %v", want, ecos)
		}
	}

	// --- git phase degraded cleanly (no .git in fixture) ------------------
	if len(res.GitRecords) != 0 {
		t.Errorf("expected 0 git records for a non-git fixture, got %d", len(res.GitRecords))
	}

	// --- health computed --------------------------------------------------
	var healthMetrics int
	_ = conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM health_file_metrics WHERE repository_id = ?`, repo.ID).Scan(&healthMetrics)
	if healthMetrics == 0 {
		t.Error("no health_file_metrics persisted")
	}
}

// TestSampleRepo_Idempotent re-runs the pipeline twice and asserts the
// persisted graph-node count is identical — ReplaceAll semantics must
// not accumulate duplicates across runs.
func TestSampleRepo_Idempotent(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(tmp, "wiki.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	repoPath, _ := filepath.Abs("../../testdata/sample-repo")
	repo, _ := repos.New(conn).EnsureByLocalPath(ctx, repoPath, "sample-repo")

	count := func() int {
		var n int
		_ = conn.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM graph_nodes WHERE repository_id = ?`, repo.ID).Scan(&n)
		return n
	}

	run := func() {
		if _, err := pipeline.Run(ctx, pipeline.Options{
			RepoPath: repoPath, Mode: pipeline.ModeInit, DB: conn, RepositoryID: repo.ID,
		}); err != nil {
			t.Fatalf("run: %v", err)
		}
	}

	run()
	first := count()
	run()
	second := count()

	if first == 0 {
		t.Fatal("first run produced no graph nodes")
	}
	if first != second {
		t.Errorf("graph_nodes count drifted across runs: %d → %d (ReplaceAll should be idempotent)", first, second)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ensure sql import stays referenced even if assertions change.
var _ = sql.ErrNoRows
