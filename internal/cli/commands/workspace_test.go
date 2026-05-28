package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workspaceTempRoot returns a tmp dir to use as a workspace root.
func workspaceTempRoot(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// addSiblingRepo creates a directory with a .git entry — passes as a
// "real git repo" for the scan path.
func addSiblingRepo(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWorkspace_ListEmpty(t *testing.T) {
	root := workspaceTempRoot(t)
	stdout, _, err := runVorCmd(t, nil, "workspace", "list", "--root", root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "no repos registered") {
		t.Errorf("expected empty state message: %s", stdout)
	}
}

func TestWorkspace_AddAndList(t *testing.T) {
	root := workspaceTempRoot(t)
	repo := addSiblingRepo(t, root, "api")
	stdout, _, err := runVorCmd(t, nil, "workspace", "add", repo, "--root", root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "added api") {
		t.Errorf("add output = %q", stdout)
	}
	list, _, _ := runVorCmd(t, nil, "workspace", "list", "--root", root)
	if !strings.Contains(list, "api") {
		t.Errorf("list missing api: %q", list)
	}
	// First-added entry is the default → marker '*'
	if !strings.Contains(list, "*") {
		t.Errorf("expected default marker '*': %q", list)
	}
}

func TestWorkspace_AddWithExplicitAlias(t *testing.T) {
	root := workspaceTempRoot(t)
	repo := addSiblingRepo(t, root, "api")
	if _, _, err := runVorCmd(t, nil,
		"workspace", "add", repo, "--alias", "backend", "--root", root); err != nil {
		t.Fatal(err)
	}
	list, _, _ := runVorCmd(t, nil, "workspace", "list", "--root", root)
	if !strings.Contains(list, "backend") {
		t.Errorf("--alias backend not respected: %q", list)
	}
}

func TestWorkspace_AddRejectsNonDir(t *testing.T) {
	root := workspaceTempRoot(t)
	// Create a regular file.
	f := filepath.Join(root, "not-a-dir")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	_, _, err := runVorCmd(t, nil, "workspace", "add", f, "--root", root)
	if err == nil {
		t.Fatal("expected error when path is not a directory")
	}
}

func TestWorkspace_Remove(t *testing.T) {
	root := workspaceTempRoot(t)
	repo := addSiblingRepo(t, root, "api")
	_, _, _ = runVorCmd(t, nil, "workspace", "add", repo, "--root", root)
	if _, _, err := runVorCmd(t, nil,
		"workspace", "remove", "api", "--root", root); err != nil {
		t.Fatal(err)
	}
	list, _, _ := runVorCmd(t, nil, "workspace", "list", "--root", root)
	if !strings.Contains(list, "no repos registered") {
		t.Errorf("expected empty state after remove: %q", list)
	}
}

func TestWorkspace_RemoveMissingErrors(t *testing.T) {
	root := workspaceTempRoot(t)
	_, _, err := runVorCmd(t, nil, "workspace", "remove", "ghost", "--root", root)
	if err == nil {
		t.Error("expected error removing nonexistent alias")
	}
}

func TestWorkspace_SetDefault(t *testing.T) {
	root := workspaceTempRoot(t)
	api := addSiblingRepo(t, root, "api")
	web := addSiblingRepo(t, root, "web")
	_, _, _ = runVorCmd(t, nil, "workspace", "add", api, "--root", root)
	_, _, _ = runVorCmd(t, nil, "workspace", "add", web, "--root", root)

	if _, _, err := runVorCmd(t, nil,
		"workspace", "set-default", "web", "--root", root); err != nil {
		t.Fatal(err)
	}
	list, _, _ := runVorCmd(t, nil, "workspace", "list", "--root", root)
	// Find the line with web and verify it has the * marker.
	for _, line := range strings.Split(list, "\n") {
		if strings.Contains(line, "web") && !strings.Contains(line, "*") {
			t.Errorf("web should be the default after set-default: %q", line)
		}
	}
}

func TestWorkspace_ScanDryRun(t *testing.T) {
	root := workspaceTempRoot(t)
	addSiblingRepo(t, root, "api")
	addSiblingRepo(t, root, "web")
	stdout, _, err := runVorCmd(t, nil, "workspace", "scan", "--root", root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "found 2 git repos") {
		t.Errorf("scan should find 2 repos: %q", stdout)
	}
	if !strings.Contains(stdout, "--yes to register") {
		t.Errorf("dry-run scan should suggest --yes: %q", stdout)
	}
}

func TestWorkspace_ScanWithYesRegisters(t *testing.T) {
	root := workspaceTempRoot(t)
	addSiblingRepo(t, root, "api")
	addSiblingRepo(t, root, "web")
	if _, _, err := runVorCmd(t, nil,
		"workspace", "scan", "--yes", "--root", root); err != nil {
		t.Fatal(err)
	}
	list, _, _ := runVorCmd(t, nil, "workspace", "list", "--root", root)
	if !strings.Contains(list, "api") || !strings.Contains(list, "web") {
		t.Errorf("scan --yes should have registered both: %q", list)
	}
}
