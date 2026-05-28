package runner_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// dirFixture sets up a repo with files in two distinct directories and
// seeds graph_nodes so loadIndexedDirectories has something to find.
func dirFixture(t *testing.T, files map[string]string) (string, string, *sql.DB) {
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
	r, _ := repos.New(conn).EnsureByLocalPath(ctx, tmp, "dir-test")

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
	}
	return tmp, r.ID, conn
}

func TestRun_DirectoryOverviews(t *testing.T) {
	root, repoID, conn := dirFixture(t, map[string]string{
		"pkg/a/file1.go": "package a\n",
		"pkg/a/file2.go": "package a\n",
		"pkg/b/file1.go": "package b\n",
	})
	prov, _ := providers.NewProvider("mock", providers.Options{})

	summary, err := runner.Run(context.Background(), runner.Options{
		RepoRoot:     root,
		RepositoryID: repoID,
		DB:           conn,
		Provider:     prov,
		Kinds:        []models.PageKind{models.PageKindDirectoryOverview},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Expect one page per distinct directory: pkg, pkg/a, pkg/b. The
	// container-only parent "pkg" is included because directories that
	// hold only subdirs are architecturally meaningful (e.g.
	// internal/generation in the port itself).
	if summary.GeneratedCount != 3 {
		t.Fatalf("GeneratedCount = %d, want 3 (pkg, pkg/a, pkg/b): %+v",
			summary.GeneratedCount, dirPaths(summary.Files))
	}
	gotPaths := dirPaths(summary.Files)
	sort.Strings(gotPaths)
	want := []string{"pkg", "pkg/a", "pkg/b"}
	if len(gotPaths) != len(want) {
		t.Errorf("directory paths = %v, want %v", gotPaths, want)
	} else {
		for i := range want {
			if gotPaths[i] != want[i] {
				t.Errorf("directory paths = %v, want %v", gotPaths, want)
				break
			}
		}
	}
}

func TestRun_DirectoryOverviews_SubdirsListed(t *testing.T) {
	// Generate the parent directory only — its bundle should list the
	// child directories.
	root, repoID, conn := dirFixture(t, map[string]string{
		"pkg/a/file1.go": "package a\n",
		"pkg/b/file1.go": "package b\n",
	})
	prov, _ := providers.NewProvider("mock", providers.Options{})

	summary, err := runner.Run(context.Background(), runner.Options{
		RepoRoot:     root,
		RepositoryID: repoID,
		DB:           conn,
		Provider:     prov,
		Kinds:        []models.PageKind{models.PageKindDirectoryOverview},
		Target:       "pkg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.GeneratedCount != 1 {
		t.Fatalf("--target pkg: GeneratedCount = %d, want 1", summary.GeneratedCount)
	}
	// Inspect the stored page's metadata to verify subdirs were
	// surfaced. The mock provider echoes back the user message, so a
	// substring search on Content is the easiest check.
	store := wikistore.New(conn)
	p, err := store.GetByTarget(context.Background(), repoID, models.PageKindDirectoryOverview, "pkg")
	if err != nil {
		t.Fatal(err)
	}
	if p.Metadata["subdir_count"] != "2" {
		t.Errorf("subdir_count = %q, want 2", p.Metadata["subdir_count"])
	}
}

func TestRun_DirectoryOverviews_SkipsWhenFresh(t *testing.T) {
	root, repoID, conn := dirFixture(t, map[string]string{
		"pkg/file.go": "package pkg\n",
	})
	prov, _ := providers.NewProvider("mock", providers.Options{})
	opts := runner.Options{
		RepoRoot: root, RepositoryID: repoID, DB: conn, Provider: prov,
		Kinds: []models.PageKind{models.PageKindDirectoryOverview},
	}
	first, _ := runner.Run(context.Background(), opts)
	if first.GeneratedCount != 1 {
		t.Fatalf("first run: %d generated", first.GeneratedCount)
	}
	second, _ := runner.Run(context.Background(), opts)
	if second.SkippedCount != 1 {
		t.Errorf("second run: SkippedCount = %d, want 1", second.SkippedCount)
	}
	if second.GeneratedCount != 0 {
		t.Errorf("second run: GeneratedCount = %d, want 0", second.GeneratedCount)
	}
}

func TestRun_DirectoryOverviews_DryRun(t *testing.T) {
	root, repoID, conn := dirFixture(t, map[string]string{
		"pkg/file.go": "package pkg\n",
	})
	summary, err := runner.Run(context.Background(), runner.Options{
		RepoRoot:     root,
		RepositoryID: repoID,
		DB:           conn,
		Kinds:        []models.PageKind{models.PageKindDirectoryOverview},
		DryRun:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.DryRunCount != 1 {
		t.Errorf("DryRunCount = %d, want 1", summary.DryRunCount)
	}
}

func TestRun_MultipleKindsInOneRun(t *testing.T) {
	root, repoID, conn := dirFixture(t, map[string]string{
		"pkg/file.go": "package pkg\n",
	})
	prov, _ := providers.NewProvider("mock", providers.Options{})
	summary, err := runner.Run(context.Background(), runner.Options{
		RepoRoot:     root,
		RepositoryID: repoID,
		DB:           conn,
		Provider:     prov,
		Kinds: []models.PageKind{
			models.PageKindFileOverview,
			models.PageKindDirectoryOverview,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// One file + one directory = 2 pages.
	if summary.GeneratedCount != 2 {
		t.Fatalf("multi-kind GeneratedCount = %d, want 2: %+v", summary.GeneratedCount, summary.Files)
	}
}

func dirPaths(rs []runner.FileResult) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Path)
	}
	return out
}
