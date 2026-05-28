package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/HundredAcreStudio/vor/internal/workspace"
)

// gitRepoWithCommits creates a real git repo at dir with the supplied
// commits. Each commit is described by (author email, when, [file:body]).
type fakeCommit struct {
	author string
	when   time.Time
	files  map[string]string
}

func gitRepoWithCommits(t *testing.T, dir string, commits []fakeCommit) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range commits {
		for rel, body := range c.files {
			full := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := wt.Add(rel); err != nil {
				t.Fatal(err)
			}
		}
		_, err := wt.Commit("test", &gogit.CommitOptions{
			AllowEmptyCommits: false,
			Author: &object.Signature{
				Name:  c.author,
				Email: c.author + "@example",
				When:  c.when,
			},
		})
		if err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
}

func TestCoChange_DetectsPairsWithinWindow(t *testing.T) {
	tmp := t.TempDir()
	apiPath := filepath.Join(tmp, "api")
	webPath := filepath.Join(tmp, "web")

	t0 := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	gitRepoWithCommits(t, apiPath, []fakeCommit{
		{author: "alice", when: t0, files: map[string]string{"api.go": "v1"}},
		{author: "alice", when: t0.Add(2 * time.Hour), files: map[string]string{"api.go": "v2"}},
		{author: "alice", when: t0.Add(10 * 24 * time.Hour), files: map[string]string{"api.go": "v3"}},
	})
	gitRepoWithCommits(t, webPath, []fakeCommit{
		// 30s after the first api commit — within the 10-minute window.
		{author: "alice", when: t0.Add(30 * time.Second), files: map[string]string{"web.ts": "v1"}},
		{author: "alice", when: t0.Add(2*time.Hour + 30*time.Second), files: map[string]string{"web.ts": "v2"}},
		{author: "alice", when: t0.Add(10*24*time.Hour + time.Hour), files: map[string]string{"unrelated.ts": "u"}},
	})

	report, err := workspace.DetectCrossRepoCoChanges(context.Background(),
		[]workspace.Entry{
			{Alias: "api", Path: apiPath},
			{Alias: "web", Path: webPath},
		},
		workspace.DetectOptions{WindowMinutes: 10, MinCount: 2})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(report.Pairs) == 0 {
		t.Fatal("expected at least one co-change pair")
	}
	top := report.Pairs[0]
	if top.Count < 2 {
		t.Errorf("top pair count = %d, want ≥2", top.Count)
	}
	// The pair should be (api: api.go) ↔ (web: web.ts).
	if !(top.RepoA == "api" && top.RepoB == "web" &&
		top.FileA == "api.go" && top.FileB == "web.ts") {
		t.Errorf("unexpected top pair: %+v", top)
	}
}

func TestCoChange_RespectsMinCount(t *testing.T) {
	tmp := t.TempDir()
	apiPath := filepath.Join(tmp, "api")
	webPath := filepath.Join(tmp, "web")

	t0 := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	// Only ONE cross-repo coincidence — should be filtered out by min_count=2.
	gitRepoWithCommits(t, apiPath, []fakeCommit{
		{author: "alice", when: t0, files: map[string]string{"a.go": "x"}},
	})
	gitRepoWithCommits(t, webPath, []fakeCommit{
		{author: "alice", when: t0.Add(time.Minute), files: map[string]string{"b.ts": "y"}},
	})

	report, _ := workspace.DetectCrossRepoCoChanges(context.Background(),
		[]workspace.Entry{
			{Alias: "api", Path: apiPath},
			{Alias: "web", Path: webPath},
		},
		workspace.DetectOptions{WindowMinutes: 10, MinCount: 2})
	if len(report.Pairs) != 0 {
		t.Errorf("expected 0 pairs (only 1 co-change, min_count=2), got %d", len(report.Pairs))
	}
}

func TestCoChange_OutsideWindowIgnored(t *testing.T) {
	tmp := t.TempDir()
	apiPath := filepath.Join(tmp, "api")
	webPath := filepath.Join(tmp, "web")

	t0 := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	// Same author but one hour apart — outside a 10-min window.
	gitRepoWithCommits(t, apiPath, []fakeCommit{
		{author: "alice", when: t0, files: map[string]string{"a.go": "1"}},
		{author: "alice", when: t0.Add(time.Hour), files: map[string]string{"a.go": "2"}},
	})
	gitRepoWithCommits(t, webPath, []fakeCommit{
		{author: "alice", when: t0.Add(30 * time.Minute), files: map[string]string{"b.ts": "1"}},
		{author: "alice", when: t0.Add(90 * time.Minute), files: map[string]string{"b.ts": "2"}},
	})
	// With window=10, none of the (api, web) commits are within the window.
	report, _ := workspace.DetectCrossRepoCoChanges(context.Background(),
		[]workspace.Entry{
			{Alias: "api", Path: apiPath},
			{Alias: "web", Path: webPath},
		},
		workspace.DetectOptions{WindowMinutes: 10, MinCount: 1})
	if len(report.Pairs) != 0 {
		t.Errorf("expected 0 pairs outside window, got %d: %+v", len(report.Pairs), report.Pairs)
	}
}

func TestCoChange_DifferentAuthorsNotPaired(t *testing.T) {
	tmp := t.TempDir()
	apiPath := filepath.Join(tmp, "api")
	webPath := filepath.Join(tmp, "web")

	t0 := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	gitRepoWithCommits(t, apiPath, []fakeCommit{
		{author: "alice", when: t0, files: map[string]string{"a.go": "1"}},
		{author: "alice", when: t0.Add(time.Hour), files: map[string]string{"a.go": "2"}},
	})
	gitRepoWithCommits(t, webPath, []fakeCommit{
		// Different author — should NOT be considered the same logical change.
		{author: "bob", when: t0.Add(time.Minute), files: map[string]string{"b.ts": "1"}},
		{author: "bob", when: t0.Add(time.Hour + time.Minute), files: map[string]string{"b.ts": "2"}},
	})
	report, _ := workspace.DetectCrossRepoCoChanges(context.Background(),
		[]workspace.Entry{
			{Alias: "api", Path: apiPath},
			{Alias: "web", Path: webPath},
		},
		workspace.DetectOptions{WindowMinutes: 10, MinCount: 1})
	if len(report.Pairs) != 0 {
		t.Errorf("different authors should not co-change-pair: %+v", report.Pairs)
	}
}

func TestCoChange_SaveLoadRoundtrip(t *testing.T) {
	tmp := t.TempDir()
	original := &workspace.CoChangeReport{
		GeneratedAt: "2024-01-01T00:00:00Z",
		Members:     []string{"api", "web"},
		Window:      10,
		MinCount:    2,
		Pairs: []workspace.CoChangePair{
			{RepoA: "api", FileA: "a.go", RepoB: "web", FileB: "b.ts", Count: 5, LastSeenAt: "2024-01-15"},
		},
	}
	if err := workspace.SaveReport(tmp, original); err != nil {
		t.Fatal(err)
	}
	loaded, err := workspace.LoadReport(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || len(loaded.Pairs) != 1 || loaded.Pairs[0].Count != 5 {
		t.Errorf("roundtrip lost data: %+v", loaded)
	}
}

func TestCoChange_LoadReportMissingFile(t *testing.T) {
	r, err := workspace.LoadReport(t.TempDir())
	if err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
	if r != nil {
		t.Errorf("expected nil report when no cache, got %+v", r)
	}
}
