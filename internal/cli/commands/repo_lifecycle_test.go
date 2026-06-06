package commands_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/cli/commands"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"

	// Side-effect imports so the pipeline registries are populated.
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/gomod"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/golang"
)

// runVorCmd executes the root command against a fresh argv/env.
// Returns captured stdout + stderr.
func runVorCmd(t *testing.T, env map[string]string, args ...string) (string, string, error) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	root := commands.Root()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), err
}

// repoFixture: tmp dir + DB URL env wired so commands open that DB.
func repoFixture(t *testing.T) (string, string, *sql.DB) {
	t.Helper()
	tmp := t.TempDir()
	dbURL := "sqlite:" + filepath.Join(tmp, "wiki.db")
	t.Setenv("VOR_DB_URL", dbURL)

	// Apply migrations + seed a Go file so init has something to do.
	conn, dialect, err := db.Open(context.Background(), db.OpenOptions{URL: dbURL})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(context.Background(), conn, dialect); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	writeFile := func(rel, body string) {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("main.go", "package main\nfunc main(){}\n")
	writeFile("go.mod", "module example.com/x\ngo 1.21\n")
	return tmp, dbURL, conn
}

func TestInit_RunsPipeline(t *testing.T) {
	tmp, _, conn := repoFixture(t)
	stdout, _, err := runVorCmd(t, nil, "init", tmp)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(stdout, "files indexed") {
		t.Errorf("expected 'files indexed' in output, got: %s", stdout)
	}
	// Repo row should exist with persisted state.
	all, _ := repos.New(conn).List(context.Background())
	if len(all) != 1 {
		t.Errorf("expected 1 repo, got %d", len(all))
	}
}

func TestDelete_RequiresConfirm(t *testing.T) {
	tmp, _, conn := repoFixture(t)
	// First index to create the repo row.
	if _, _, err := runVorCmd(t, nil, "init", tmp); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := runVorCmd(t, nil, "delete", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Re-run with --yes") {
		t.Errorf("expected confirm prompt, got: %s", stdout)
	}
	// Repo should still exist.
	all, _ := repos.New(conn).List(context.Background())
	if len(all) != 1 {
		t.Errorf("delete without --yes should be a no-op, got %d repos", len(all))
	}
}

func TestDelete_WithConfirmRemovesRepo(t *testing.T) {
	tmp, _, conn := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "init", tmp); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runVorCmd(t, nil, "delete", "--yes", tmp); err != nil {
		t.Fatal(err)
	}
	all, _ := repos.New(conn).List(context.Background())
	if len(all) != 0 {
		t.Errorf("expected 0 repos after delete --yes, got %d", len(all))
	}
}

func TestDelete_CascadesPersistedTables(t *testing.T) {
	tmp, _, conn := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "init", tmp); err != nil {
		t.Fatal(err)
	}
	// Verify some persisted state exists before delete.
	var preCount int
	if err := conn.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM graph_nodes").Scan(&preCount); err != nil {
		t.Fatal(err)
	}
	if preCount == 0 {
		t.Fatal("expected graph_nodes populated after update")
	}

	if _, _, err := runVorCmd(t, nil, "delete", "--yes", tmp); err != nil {
		t.Fatal(err)
	}
	var postCount int
	if err := conn.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM graph_nodes").Scan(&postCount); err != nil {
		t.Fatal(err)
	}
	if postCount != 0 {
		t.Errorf("expected graph_nodes empty after delete, got %d rows", postCount)
	}
}

// The default reindex is non-destructive and runs without confirmation.
func TestReindex_DefaultIsNonDestructive(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "init", tmp); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runVorCmd(t, nil, "reindex", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "--hard") {
		t.Errorf("default reindex should not prompt for confirmation, got %q", stdout)
	}
	if !strings.Contains(stdout, "reindex complete") {
		t.Errorf("expected default reindex to run, got %q", stdout)
	}
}

// The destructive --hard rebuild requires --yes.
func TestReindexHard_RequiresConfirm(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "init", tmp); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runVorCmd(t, nil, "reindex", "--hard", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "--hard --yes") {
		t.Errorf("expected --hard to require confirmation, got %q", stdout)
	}
}

func TestReindex_RebuildsFromScratch(t *testing.T) {
	tmp, _, conn := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "init", tmp); err != nil {
		t.Fatal(err)
	}
	var oldID string
	_ = conn.QueryRowContext(context.Background(),
		"SELECT id FROM repositories LIMIT 1").Scan(&oldID)

	// A repo-scoped setting keyed on the repo id. It has no FK to
	// repositories, so it must survive a reindex AND stay attached to the
	// (preserved) id — otherwise it silently orphans.
	if _, err := conn.ExecContext(context.Background(),
		`INSERT INTO settings (repository_id, key, value) VALUES (?, ?, ?)`,
		oldID, "health_rules", `[{"pattern":"**/*_test.go","overrides":{"brain_method":"disabled"}}]`,
	); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := runVorCmd(t, nil, "reindex", "--hard", "--yes", tmp)
	if err != nil {
		t.Fatalf("reindex --hard --yes: %v", err)
	}
	if !strings.Contains(stdout, "reindex complete") {
		t.Errorf("expected 'reindex complete', got %q", stdout)
	}

	// The rebuild must wipe derived state but PRESERVE the repo identity, so
	// repo-scoped settings keep matching.
	var newID string
	_ = conn.QueryRowContext(context.Background(),
		"SELECT id FROM repositories LIMIT 1").Scan(&newID)
	if newID == "" || newID != oldID {
		t.Errorf("expected reindex to preserve the repo id (old=%s new=%s)", oldID, newID)
	}
	var settingRepoID string
	_ = conn.QueryRowContext(context.Background(),
		"SELECT repository_id FROM settings WHERE key = 'health_rules'").Scan(&settingRepoID)
	if settingRepoID != oldID {
		t.Errorf("health_rules setting orphaned by reindex: keyed to %q, repo is %q", settingRepoID, oldID)
	}
}

// silence unused-import lint when only some tests run.
var _ = io.Discard
