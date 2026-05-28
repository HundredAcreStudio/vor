package commands_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"

	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/gomod"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/golang"
)

// makeMemberRepo creates a tiny Go repo + initialised DB so workspace
// commands have something realistic to operate on. .git is created so
// the hook subcommands can resolve the git root.
func makeMemberRepo(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"main.go": "package main\nfunc main(){}\n",
		"go.mod":  "module example.com/" + name + "\ngo 1.21\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Pre-create a per-repo DB so openDB resolves to <repo>/.vor/wiki.db.
	dbPath := filepath.Join(dir, ".vor", "wiki.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	_, _ = repos.New(conn).EnsureByLocalPath(ctx, dir, name)
	return dir
}

// workspaceWithTwoRepos sets up a workspace root with two registered
// member repos. Returns the workspace root path.
func workspaceWithTwoRepos(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"api", "web"} {
		dir := makeMemberRepo(t, root, name)
		if _, _, err := runVorCmd(t, nil, "workspace", "add", dir, "--root", root); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestWorkspaceStatus_EmptyRegistry(t *testing.T) {
	root := t.TempDir()
	stdout, _, err := runVorCmd(t, nil, "workspace", "status", "--root", root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "no repos registered") {
		t.Errorf("expected empty registry msg: %q", stdout)
	}
}

func TestWorkspaceStatus_NotYetIndexed(t *testing.T) {
	root := workspaceWithTwoRepos(t)
	stdout, _, err := runVorCmd(t, nil, "workspace", "status", "--root", root)
	if err != nil {
		t.Fatal(err)
	}
	// Both repos appear in the table.
	for _, alias := range []string{"api", "web"} {
		if !strings.Contains(stdout, alias) {
			t.Errorf("status missing alias %q: %s", alias, stdout)
		}
	}
	// No pipeline run yet → "no" in the indexed column.
	if strings.Count(stdout, "  no  ") < 2 {
		t.Errorf("expected both repos to show 'no' for indexed: %s", stdout)
	}
}

func TestWorkspaceUpdate_RunsOnAllMembers(t *testing.T) {
	root := workspaceWithTwoRepos(t)
	stdout, _, err := runVorCmd(t, nil, "workspace", "update", "--root", root)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	for _, alias := range []string{"api", "web"} {
		if !strings.Contains(stdout, alias+": ok") {
			t.Errorf("expected '%s: ok' in output: %s", alias, stdout)
		}
	}
	if !strings.Contains(stdout, "2 updated, 0 failed") {
		t.Errorf("expected '2 updated, 0 failed': %s", stdout)
	}
}

func TestWorkspaceStatus_AfterUpdate(t *testing.T) {
	root := workspaceWithTwoRepos(t)
	if _, _, err := runVorCmd(t, nil, "workspace", "update", "--root", root); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ := runVorCmd(t, nil, "workspace", "status", "--root", root)
	// After update, both should show as indexed.
	if strings.Count(stdout, "  yes  ") < 2 {
		t.Errorf("expected both repos indexed after update: %s", stdout)
	}
	if !strings.Contains(stdout, "succeeded") {
		t.Errorf("expected 'succeeded' status: %s", stdout)
	}
}

func TestWorkspaceDoctor_FlagsNotIndexed(t *testing.T) {
	root := workspaceWithTwoRepos(t)
	stdout, _, err := runVorCmd(t, nil, "workspace", "doctor", "--root", root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "never indexed") {
		t.Errorf("doctor should flag never-indexed: %s", stdout)
	}
}

func TestWorkspaceDoctor_AllHealthyAfterUpdate(t *testing.T) {
	root := workspaceWithTwoRepos(t)
	if _, _, err := runVorCmd(t, nil, "workspace", "update", "--root", root); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ := runVorCmd(t, nil, "workspace", "doctor", "--root", root)
	// All entries should be OK.
	for _, alias := range []string{"api", "web"} {
		if !strings.Contains(stdout, alias+": OK") {
			t.Errorf("expected '%s: OK', got: %s", alias, stdout)
		}
	}
}

func TestWorkspaceHook_InstallAcrossWorkspace(t *testing.T) {
	root := workspaceWithTwoRepos(t)
	stdout, _, err := runVorCmd(t, nil, "workspace", "hook", "install", "--root", root)
	if err != nil {
		t.Fatalf("hook install: %v", err)
	}
	for _, alias := range []string{"api", "web"} {
		if !strings.Contains(stdout, alias+":") {
			t.Errorf("hook install output missing alias %q: %s", alias, stdout)
		}
	}
	// Each member repo should now have a post-commit hook.
	for _, alias := range []string{"api", "web"} {
		hookPath := filepath.Join(root, alias, ".git", "hooks", "post-commit")
		if _, err := os.Stat(hookPath); err != nil {
			t.Errorf("hook missing at %s: %v", hookPath, err)
		}
	}
}

func TestWorkspaceHook_StatusAcrossWorkspace(t *testing.T) {
	root := workspaceWithTwoRepos(t)
	stdout, _, _ := runVorCmd(t, nil, "workspace", "hook", "status", "--root", root)
	for _, alias := range []string{"api", "web"} {
		if !strings.Contains(stdout, alias+":") {
			t.Errorf("status output missing alias %q: %s", alias, stdout)
		}
	}
	if !strings.Contains(stdout, "no post-commit hook") {
		t.Errorf("expected 'no post-commit hook' before install: %s", stdout)
	}
}
