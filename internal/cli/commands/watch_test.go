package commands

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"

	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/gomod"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/golang"
)

// internalTestRepo sets up a tmp dir + DB without env-var indirection
// so we can drive runWatchLoop directly.
func internalTestRepo(t *testing.T) (string, string, *sql.DB) {
	t.Helper()
	tmp := t.TempDir()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{
		URL: "sqlite:" + filepath.Join(tmp, "wiki.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	repoRow, _ := repos.New(conn).EnsureByLocalPath(ctx, tmp, "watch-test")
	for _, f := range []struct{ name, body string }{
		{"main.go", "package main\nfunc main(){}\n"},
		{"go.mod", "module example.com/x\ngo 1.21\n"},
	} {
		_ = os.WriteFile(filepath.Join(tmp, f.name), []byte(f.body), 0o644)
	}
	return tmp, repoRow.ID, conn
}

func TestWatch_TriggersUpdateAfterDebounce(t *testing.T) {
	tmp, repoID, conn := internalTestRepo(t)

	updated := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = runWatchLoop(ctx, watchOptions{
			Root:         tmp,
			DB:           conn,
			RepositoryID: repoID,
			Debounce:     50 * time.Millisecond,
			Logger:       slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
			ProgressOut:  &bytes.Buffer{},
			OnUpdate: func() {
				select {
				case updated <- struct{}{}:
				default:
				}
			},
		})
	}()
	time.Sleep(150 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(tmp, "main.go"),
		[]byte("package main\nfunc main(){println(1)}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-updated:
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not trigger update within 5s")
	}
}

func TestWatch_IgnoresGitDirChanges(t *testing.T) {
	tmp, repoID, conn := internalTestRepo(t)
	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	updates := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = runWatchLoop(ctx, watchOptions{
			Root:         tmp,
			DB:           conn,
			RepositoryID: repoID,
			Debounce:     50 * time.Millisecond,
			Logger:       slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
			ProgressOut:  &bytes.Buffer{},
			OnUpdate:     func() { updates++ },
		})
	}()
	time.Sleep(150 * time.Millisecond)

	_ = os.WriteFile(filepath.Join(tmp, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	time.Sleep(200 * time.Millisecond)
	if updates != 0 {
		t.Errorf("expected 0 updates for .git change, got %d", updates)
	}
}

func TestWatch_IgnoresSwapFiles(t *testing.T) {
	tmp := t.TempDir()
	for _, suffix := range []string{".swp", ".swx", "~"} {
		ev := fsnotify.Event{Name: filepath.Join(tmp, "foo"+suffix), Op: fsnotify.Write}
		if !shouldIgnoreEvent(ev, tmp) {
			t.Errorf("expected %s suffix to be ignored", suffix)
		}
	}
}

func TestWatch_isIgnoredDir(t *testing.T) {
	cases := map[string]bool{
		"src":           false,
		"internal":      false,
		".git":          true,
		"node_modules":  true,
		"vendor":        true,
		"build":         true,
		".vor":          true,
		".pytest_cache": true,
		"foo":           false,
	}
	for name, want := range cases {
		if got := isIgnoredDir(name); got != want {
			t.Errorf("isIgnoredDir(%q) = %v, want %v", name, got, want)
		}
	}
}
