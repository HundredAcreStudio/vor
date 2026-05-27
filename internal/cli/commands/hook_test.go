package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepoFixture creates a tmp dir + an empty .git directory so the
// hook resolver can walk to it. Doesn't run `git init` — the resolver
// only checks for .git/, not for a valid repo.
func gitRepoFixture(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return tmp
}

func TestHook_StatusBeforeInstall(t *testing.T) {
	tmp := gitRepoFixture(t)
	stdout, _, err := runRepowiseCmd(t, nil, "hook", "status", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "no post-commit hook installed") {
		t.Errorf("status before install = %q", stdout)
	}
}

func TestHook_InstallCreatesFile(t *testing.T) {
	tmp := gitRepoFixture(t)
	stdout, _, err := runRepowiseCmd(t, nil, "hook", "install", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "hook installed at") {
		t.Errorf("install output = %q", stdout)
	}
	body, err := os.ReadFile(filepath.Join(tmp, ".git", "hooks", "post-commit"))
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	s := string(body)
	for _, want := range []string{
		"#!/bin/sh",
		"# repowise-hook-start",
		"# repowise-hook-end",
		"repowise update",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("hook body missing %q", want)
		}
	}
	// Hook should be executable.
	info, _ := os.Stat(filepath.Join(tmp, ".git", "hooks", "post-commit"))
	if info.Mode()&0o111 == 0 {
		t.Errorf("hook not executable: mode=%v", info.Mode())
	}
}

func TestHook_InstallIsIdempotent(t *testing.T) {
	tmp := gitRepoFixture(t)
	for i := 0; i < 3; i++ {
		if _, _, err := runRepowiseCmd(t, nil, "hook", "install", tmp); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}
	body, _ := os.ReadFile(filepath.Join(tmp, ".git", "hooks", "post-commit"))
	s := string(body)
	if got := strings.Count(s, "# repowise-hook-start"); got != 1 {
		t.Errorf("# repowise-hook-start count = %d after 3 installs, want 1", got)
	}
	if got := strings.Count(s, "# repowise-hook-end"); got != 1 {
		t.Errorf("# repowise-hook-end count = %d after 3 installs, want 1", got)
	}
}

func TestHook_InstallPreservesExistingHookContent(t *testing.T) {
	tmp := gitRepoFixture(t)
	hookPath := filepath.Join(tmp, ".git", "hooks", "post-commit")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "#!/bin/sh\n# user-installed hook below\necho 'pre-existing'\n"
	if err := os.WriteFile(hookPath, []byte(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runRepowiseCmd(t, nil, "hook", "install", tmp); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(hookPath)
	s := string(body)
	if !strings.Contains(s, "echo 'pre-existing'") {
		t.Errorf("pre-existing hook content was clobbered: %q", s)
	}
	if !strings.Contains(s, "# repowise-hook-start") {
		t.Errorf("repowise block not appended")
	}
}

func TestHook_StatusAfterInstall(t *testing.T) {
	tmp := gitRepoFixture(t)
	if _, _, err := runRepowiseCmd(t, nil, "hook", "install", tmp); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runRepowiseCmd(t, nil, "hook", "status", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "repowise hook installed at") {
		t.Errorf("status after install = %q", stdout)
	}
}

func TestHook_UninstallRemovesBlock(t *testing.T) {
	tmp := gitRepoFixture(t)
	if _, _, err := runRepowiseCmd(t, nil, "hook", "install", tmp); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runRepowiseCmd(t, nil, "hook", "uninstall", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "removed") {
		t.Errorf("uninstall output = %q", stdout)
	}
	// With nothing else in the hook, the file should have been deleted.
	if _, err := os.Stat(filepath.Join(tmp, ".git", "hooks", "post-commit")); !os.IsNotExist(err) {
		t.Errorf("expected hook file deleted after stripping otherwise-empty block")
	}
}

func TestHook_UninstallPreservesOtherContent(t *testing.T) {
	tmp := gitRepoFixture(t)
	hookPath := filepath.Join(tmp, ".git", "hooks", "post-commit")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath,
		[]byte("#!/bin/sh\necho 'user thing'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runRepowiseCmd(t, nil, "hook", "install", tmp); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runRepowiseCmd(t, nil, "hook", "uninstall", tmp); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(hookPath)
	s := string(body)
	if !strings.Contains(s, "echo 'user thing'") {
		t.Errorf("user content clobbered on uninstall: %q", s)
	}
	if strings.Contains(s, "repowise-hook-start") {
		t.Error("repowise markers survived uninstall")
	}
}

func TestHook_UninstallWhenAbsent(t *testing.T) {
	tmp := gitRepoFixture(t)
	stdout, _, err := runRepowiseCmd(t, nil, "hook", "uninstall", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "no post-commit hook found") {
		t.Errorf("uninstall with no hook = %q", stdout)
	}
}

func TestHook_FailsWithoutGitDir(t *testing.T) {
	tmp := t.TempDir() // no .git
	_, _, err := runRepowiseCmd(t, nil, "hook", "install", tmp)
	if err == nil {
		t.Fatal("expected error when no .git found")
	}
	if !strings.Contains(err.Error(), "no .git") {
		t.Errorf("expected 'no .git' in error, got %v", err)
	}
}
