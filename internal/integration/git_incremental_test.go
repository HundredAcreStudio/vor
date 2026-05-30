package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/pipeline"
)

// TestPipeline_GitReusedWhenHeadUnchanged verifies the commit-boundary
// behavior: the first index walks git history; re-indexing the same HEAD skips
// the walk and reuses the stored git_metadata (GitReused), while a new commit
// forces a fresh walk.
func TestPipeline_GitReusedWhenHeadUnchanged(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	r, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	commit := func(file, content, msg string) {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(file); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Commit(msg, &gogit.CommitOptions{
			Author: &object.Signature{Name: "t", Email: "t@x", When: time.Now()},
		}); err != nil {
			t.Fatal(err)
		}
	}
	commit("main.go", "package main\n\nfunc main() {}\n", "init")

	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(dir, "i.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	repo, err := repos.New(conn).EnsureByLocalPath(ctx, dir, "inc")
	if err != nil {
		t.Fatal(err)
	}

	run := func(mode pipeline.Mode) *pipeline.Result {
		res, err := pipeline.Run(ctx, pipeline.Options{
			RepoPath: dir, Mode: mode, DB: conn, RepositoryID: repo.ID,
		})
		if err != nil {
			t.Fatalf("pipeline.Run: %v", err)
		}
		return res
	}

	// First index: walks history.
	if first := run(pipeline.ModeInit); first.GitReused {
		t.Error("first index should walk git history, not reuse")
	}

	// Re-index at the same HEAD: reuses stored git metadata.
	if second := run(pipeline.ModeUpdate); !second.GitReused {
		t.Error("re-index at unchanged HEAD should reuse stored git metadata")
	}

	// A new commit moves HEAD: walk again.
	commit("other.go", "package main\n\nfunc Other() {}\n", "second")
	if third := run(pipeline.ModeUpdate); third.GitReused {
		t.Error("new commit should force a fresh git walk")
	}
}
