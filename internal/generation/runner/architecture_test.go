package runner_test

import (
	"context"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/generation/models"
	"github.com/HundredAcreStudio/vor/internal/generation/pages"
	"github.com/HundredAcreStudio/vor/internal/generation/runner"
	"github.com/HundredAcreStudio/vor/internal/persistence/wikistore"
)

func TestRun_Architecture(t *testing.T) {
	ctx := context.Background()
	root, repoID, conn := fixture(t, map[string]string{
		"cmd/app/main.go":     "package main\nfunc main(){}\n",
		"internal/svc/svc.go": "package svc\nfunc Do(){}\n",
	})

	opts := runner.Options{
		RepoRoot:     root,
		RepositoryID: repoID,
		DB:           conn,
		Provider:     mockProvider(t),
		Model:        "mock-1",
		Kinds:        []models.PageKind{models.PageKindArchitecture},
	}

	// First run generates exactly one repo-wide overview page.
	sum, err := runner.Run(ctx, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sum.GeneratedCount != 1 {
		t.Fatalf("generated = %d, want 1; files=%+v", sum.GeneratedCount, sum.Files)
	}

	page, err := wikistore.New(conn).GetByTarget(ctx, repoID, models.PageKindArchitecture, pages.ArchitectureTargetPath)
	if err != nil {
		t.Fatalf("architecture page not persisted: %v", err)
	}
	if page.Content == "" || page.Title == "" {
		t.Errorf("architecture page missing content/title: %+v", page)
	}
	if page.SourceHash == "" {
		t.Error("architecture page should carry a source hash for incremental regen")
	}

	// Re-running with unchanged structure skips (incremental).
	sum2, err := runner.Run(ctx, opts)
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if sum2.SkippedCount != 1 || sum2.GeneratedCount != 0 {
		t.Errorf("rerun: generated=%d skipped=%d, want 0/1", sum2.GeneratedCount, sum2.SkippedCount)
	}
}
