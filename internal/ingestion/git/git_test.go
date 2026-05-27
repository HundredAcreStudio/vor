package git_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/repowise-dev/repowise-go/internal/ingestion/git"
)

// makeTempRepo creates a real git repository in t.TempDir() and returns its
// path plus a commit helper. The helper accepts a relative file path, the
// content to put in it, the author name+email, and an optional commit
// time (zero = now).
type committer func(t *testing.T, path, content, author, email string, when time.Time)

func makeTempRepo(t *testing.T) (string, committer) {
	t.Helper()
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}

	commit := func(t *testing.T, path, content, author, email string, when time.Time) {
		t.Helper()
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(path); err != nil {
			t.Fatalf("Add %s: %v", path, err)
		}
		if when.IsZero() {
			when = time.Now()
		}
		_, err := wt.Commit("touch "+path, &gogit.CommitOptions{
			Author: &object.Signature{Name: author, Email: email, When: when},
		})
		if err != nil {
			t.Fatalf("Commit %s: %v", path, err)
		}
	}
	return dir, commit
}

func TestIndex_BasicCountsAndOwnership(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	repo, commit := makeTempRepo(t)

	// alice commits foo.go three times within the last 30 days; bob commits
	// once 100 days ago.
	commit(t, "foo.go", "v1\n", "Alice", "alice@example.com", now.AddDate(0, 0, -1))
	commit(t, "foo.go", "v2\n", "Alice", "alice@example.com", now.AddDate(0, 0, -2))
	commit(t, "foo.go", "v3\n", "Alice", "alice@example.com", now.AddDate(0, 0, -3))
	commit(t, "foo.go", "v4\n", "Bob", "bob@example.com", now.AddDate(0, 0, -100))

	ix := &git.Indexer{Now: now}
	records, err := ix.Index(context.Background(), repo)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	var foo *git.PerFile
	for i := range records {
		if records[i].Path == "foo.go" {
			foo = &records[i]
		}
	}
	if foo == nil {
		t.Fatalf("foo.go missing from results: %+v", records)
	}

	if foo.CommitCountTotal != 4 {
		t.Errorf("CommitCountTotal = %d, want 4", foo.CommitCountTotal)
	}
	if foo.CommitCount90d != 3 {
		t.Errorf("CommitCount90d = %d, want 3 (alice's three recent commits)", foo.CommitCount90d)
	}
	if foo.CommitCount30d != 3 {
		t.Errorf("CommitCount30d = %d, want 3", foo.CommitCount30d)
	}
	if foo.PrimaryOwner == nil || foo.PrimaryOwner.Name != "Alice" {
		t.Errorf("PrimaryOwner = %+v, want Alice", foo.PrimaryOwner)
	}
	if foo.PrimaryOwner.CommitPct != 0.75 {
		t.Errorf("Alice's CommitPct = %v, want 0.75", foo.PrimaryOwner.CommitPct)
	}
	if foo.RecentOwner == nil || foo.RecentOwner.Name != "Alice" {
		t.Errorf("RecentOwner = %+v, want Alice", foo.RecentOwner)
	}
	if foo.ContributorCount != 2 {
		t.Errorf("ContributorCount = %d, want 2", foo.ContributorCount)
	}
	// Bus factor: Alice alone has 75% (>= 80% threshold? no, 0.75 < 0.80) so
	// need Alice + Bob = 1.0 ≥ 0.80 → bus factor 2.
	if foo.BusFactor != 2 {
		t.Errorf("BusFactor = %d, want 2", foo.BusFactor)
	}
	if foo.AgeDays < 100 {
		t.Errorf("AgeDays = %d, want >= 100", foo.AgeDays)
	}
}

func TestIndex_CoChangeAndHotspot(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	repo, commit := makeTempRepo(t)

	// foo.go + bar.go change together 3 times; baz.go changes alone twice.
	for i := 0; i < 3; i++ {
		commit(t, "foo.go", "f"+string(rune('A'+i)), "Alice", "a@x", now.AddDate(0, 0, -i-1))
		commit(t, "bar.go", "b"+string(rune('A'+i)), "Alice", "a@x", now.AddDate(0, 0, -i-1))
	}
	// Then a commit that touches both at once.
	{
		dir := repo
		fooPath := filepath.Join(dir, "foo.go")
		barPath := filepath.Join(dir, "bar.go")
		os.WriteFile(fooPath, []byte("co-change-foo"), 0o644)
		os.WriteFile(barPath, []byte("co-change-bar"), 0o644)
		gitRepo, _ := gogit.PlainOpen(dir)
		wt, _ := gitRepo.Worktree()
		wt.Add("foo.go")
		wt.Add("bar.go")
		wt.Commit("co-change foo+bar", &gogit.CommitOptions{
			Author: &object.Signature{Name: "Alice", Email: "a@x", When: now.AddDate(0, 0, -1)},
		})
	}

	for i := 0; i < 2; i++ {
		commit(t, "baz.go", "z"+string(rune('A'+i)), "Alice", "a@x", now.AddDate(0, 0, -i-1))
	}

	ix := &git.Indexer{Now: now, HotspotPercentile: 0.5}
	records, err := ix.Index(context.Background(), repo)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	var foo *git.PerFile
	for i := range records {
		if records[i].Path == "foo.go" {
			foo = &records[i]
		}
	}
	if foo == nil {
		t.Fatalf("foo.go missing")
	}

	// foo.go should list bar.go as a co-change partner with count 1.
	partners := make([]string, 0, len(foo.CoChangePartners))
	for _, p := range foo.CoChangePartners {
		partners = append(partners, p.Path)
	}
	if !slices.Contains(partners, "bar.go") {
		t.Errorf("foo.go's co-change partners = %v, want to contain bar.go", partners)
	}
}

func TestIndex_RespectsMaxCommits(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	repo, commit := makeTempRepo(t)
	for i := 0; i < 5; i++ {
		commit(t, "foo.go", "v"+string(rune('a'+i)), "Alice", "a@x", now.AddDate(0, 0, -i-1))
	}
	ix := &git.Indexer{Now: now, MaxCommits: 2}
	records, err := ix.Index(context.Background(), repo)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(records) != 1 || records[0].Path != "foo.go" {
		t.Errorf("expected 1 record for foo.go, got %+v", records)
	}
	if records[0].CommitCountTotal != 2 {
		t.Errorf("CommitCountTotal = %d, want 2 (cap)", records[0].CommitCountTotal)
	}
}

func TestIndex_HotspotFlag(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	repo, commit := makeTempRepo(t)

	// hot.go: 10 commits with substantial diffs in last 90d.
	for i := 0; i < 10; i++ {
		content := ""
		for j := 0; j < 50; j++ {
			content += "line\n"
		}
		commit(t, "hot.go", content+"v"+string(rune('a'+i)), "Alice", "a@x", now.AddDate(0, 0, -i-1))
	}
	// cold.go: 1 commit.
	commit(t, "cold.go", "small", "Alice", "a@x", now.AddDate(0, 0, -1))

	ix := &git.Indexer{Now: now, HotspotPercentile: 0.9}
	records, err := ix.Index(context.Background(), repo)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	var hot, cold *git.PerFile
	for i := range records {
		switch records[i].Path {
		case "hot.go":
			hot = &records[i]
		case "cold.go":
			cold = &records[i]
		}
	}
	if hot == nil || cold == nil {
		t.Fatalf("missing files: %+v", records)
	}
	if !hot.IsHotspot {
		t.Errorf("hot.go IsHotspot = false, want true (churn pctl = %v)", hot.ChurnPercentile)
	}
	if cold.IsHotspot {
		t.Errorf("cold.go IsHotspot = true, want false")
	}
}

func TestIndex_EmptyRepoErrors(t *testing.T) {
	// PlainInit without commits → no HEAD. Index should surface the error.
	dir := t.TempDir()
	if _, err := gogit.PlainInit(dir, false); err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	ix := &git.Indexer{Now: time.Now()}
	_, err := ix.Index(context.Background(), dir)
	if err == nil {
		t.Errorf("expected error on empty repo, got nil")
	}
}

func TestResolveHeadCommit(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	repo, commit := makeTempRepo(t)
	commit(t, "x.go", "x", "Alice", "a@x", now)
	hash, err := git.ResolveHeadCommit(repo)
	if err != nil {
		t.Fatalf("ResolveHeadCommit: %v", err)
	}
	if hash.IsZero() {
		t.Errorf("HEAD hash is zero")
	}
}
