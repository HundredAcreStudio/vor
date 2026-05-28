package autoindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/pipelinestore"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"

	// Register the Go parser so a reindex does real work in this test binary.
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/golang"
)

// fixture builds an on-disk repo dir + migrated DB and registers the repo,
// returning the open Watcher inputs plus the repo id and source dir.
func fixture(t *testing.T, files map[string]string) (*Watcher, string, string) {
	t.Helper()
	ctx := context.Background()
	tmp := t.TempDir()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(tmp, "wiki.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	// Source lives in its own dir so wiki.db isn't part of the repo tree.
	src := filepath.Join(tmp, "src")
	for rel, body := range files {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := repos.New(conn).EnsureByLocalPath(ctx, src, "auto")
	if err != nil {
		t.Fatal(err)
	}
	w := New(Options{DB: conn, Debounce: 100 * time.Millisecond})
	return w, r.ID, src
}

// waitForRun polls until a succeeded run exists for repoID, returning its
// run id, or fails the test if the deadline passes first.
func waitForRun(t *testing.T, w *Watcher, repoID string, timeout time.Duration) string {
	t.Helper()
	store := pipelinestore.New(w.db)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := store.LatestRun(context.Background(), repoID)
		if err == nil && run != nil && run.Overall == pipelinestore.OutcomeSucceeded {
			return run.RunID
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no succeeded run for %s within %s", repoID, timeout)
	return ""
}

func TestRun_StartupReindexes(t *testing.T) {
	w, repoID, _ := fixture(t, map[string]string{
		"main.go": "package main\nfunc main(){ helper() }\nfunc helper(){}\n",
		"go.mod":  "module example.com/x\ngo 1.21\n",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	runID := waitForRun(t, w, repoID, 15*time.Second)
	if runID == "" {
		t.Fatal("expected a startup reindex run")
	}
}

func TestRun_ReindexesOnChange(t *testing.T) {
	w, repoID, src := fixture(t, map[string]string{
		"main.go": "package main\nfunc main(){}\n",
		"go.mod":  "module example.com/x\ngo 1.21\n",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	// Wait for the startup run, then capture its id.
	startupRun := waitForRun(t, w, repoID, 15*time.Second)

	// Edit a source file; the watcher should fire a fresh reindex.
	if err := os.WriteFile(filepath.Join(src, "main.go"),
		[]byte("package main\nfunc main(){ added() }\nfunc added(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := pipelinestore.New(w.db)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		run, err := store.LatestRun(context.Background(), repoID)
		if err == nil && run != nil && run.RunID != startupRun &&
			run.Overall == pipelinestore.OutcomeSucceeded {
			return // a new run completed — change was picked up
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("file change did not trigger a new reindex run")
}

func TestSkipEvent(t *testing.T) {
	rw := &repoWatcher{root: "/repo"}
	cases := map[string]bool{
		"/repo/.vor/wiki.db":     true,
		"/repo/.vor":             true,
		"/repo/.git/index":       true,
		"/repo/.git":             true,
		"/repo/main.go":          false,
		"/repo/pkg/util/util.go": false,
		"/repo/.vorignore":       false, // a file, not the .vor/ dir
	}
	for path, want := range cases {
		if got := rw.skipEvent(path); got != want {
			t.Errorf("skipEvent(%q) = %v, want %v", path, got, want)
		}
	}
}
