package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

// TestRepoID_AddressesNonCwdRepo verifies that a read command can
// target a repo by its id without the process being cd'd into that
// repo's path — the core daemon-mode requirement.
func TestRepoID_AddressesNonCwdRepo(t *testing.T) {
	tmp, _, conn := repoFixture(t)
	// Index the fixture repo so it has a row + data.
	if _, _, err := runVorCmd(t, nil, "init", tmp); err != nil {
		t.Fatal(err)
	}
	repoRow, _ := repos.New(conn).EnsureByLocalPath(context.Background(), tmp, "")

	// Invoke `status` from a DIFFERENT working directory (the default
	// "." would resolve to cwd, not the fixture). Addressing by
	// --repo-id should still find it.
	otherDir := t.TempDir()
	stdout, _, err := runVorCmd(t, nil, "status", "--repo", otherDir, "--repo-id", repoRow.ID)
	if err != nil {
		t.Fatalf("status --repo-id: %v", err)
	}
	if !strings.Contains(stdout, repoRow.Name) {
		t.Errorf("--repo-id did not resolve to the fixture repo: %s", stdout)
	}
}

func TestRepoID_UnknownIDErrors(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "init", tmp); err != nil {
		t.Fatal(err)
	}
	_, _, err := runVorCmd(t, nil, "status", "--repo", tmp, "--repo-id", "does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown --repo-id")
	}
	if !strings.Contains(err.Error(), "no repository with id") {
		t.Errorf("error should name the missing id: %v", err)
	}
}

func TestRepoID_EmptyFallsBackToPath(t *testing.T) {
	// Without --repo-id, the existing --repo path resolution applies.
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "init", tmp); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runVorCmd(t, nil, "status", "--repo", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "graph nodes") {
		t.Errorf("path-based resolution should still work: %s", stdout)
	}
}

// TestRepoID_AcrossReadCommands smoke-checks that the flag is wired
// into every read command (they share resolveReadRepo, but the flag
// registration is per-command).
func TestRepoID_AcrossReadCommands(t *testing.T) {
	tmp, _, conn := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "init", tmp); err != nil {
		t.Fatal(err)
	}
	repoRow, _ := repos.New(conn).EnsureByLocalPath(context.Background(), tmp, "")
	other := t.TempDir()

	// Browsing commands moved to the dashboard; the read commands that
	// remain in the CLI must still honor --repo-id (they share
	// resolveReadRepo, but the flag is registered per-command).
	for _, cmd := range [][]string{
		{"status", "--repo", other, "--repo-id", repoRow.ID},
		{"search", "x", "--repo", other, "--repo-id", repoRow.ID},
	} {
		if _, _, err := runVorCmd(t, nil, cmd...); err != nil {
			t.Errorf("%v: %v", cmd, err)
		}
	}
}
