package pipeline_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/pipeline"
)

// TestIncrementalParse_ReusesUnchangedFiles is the core Phase 15a
// acceptance: after a first index, a re-index reuses every cached parse;
// editing one file re-parses only that file; deleting a file prunes it.
func TestIncrementalParse_ReusesUnchangedFiles(t *testing.T) {
	root, conn, repoID := setup(t)
	ctx := context.Background()
	// setup seeds main.go; add two more parseable files so counts are clear.
	writeFile(t, root, "a.go", "package main\nfunc A(){}\n")
	writeFile(t, root, "b.go", "package main\nfunc B(){}\n")

	run := func(mode pipeline.Mode) pipeline.ParseStats {
		t.Helper()
		res, err := pipeline.Run(ctx, pipeline.Options{
			RepoPath: root, Mode: mode, DB: conn, RepositoryID: repoID,
		})
		if err != nil {
			t.Fatalf("Run(%s): %v", mode, err)
		}
		return res.ParseStats
	}

	// First index: everything is parsed fresh, nothing reused.
	first := run(pipeline.ModeInit)
	if first.Total != 3 {
		t.Fatalf("first run total = %d, want 3 (main.go, a.go, b.go)", first.Total)
	}
	if first.Parsed != 3 || first.Reused != 0 {
		t.Errorf("first run: parsed=%d reused=%d, want 3/0", first.Parsed, first.Reused)
	}

	// Unchanged re-index: everything reused, nothing parsed.
	clean := run(pipeline.ModeUpdate)
	if clean.Reused != 3 || clean.Parsed != 0 {
		t.Errorf("unchanged update: parsed=%d reused=%d, want 0/3", clean.Parsed, clean.Reused)
	}

	// Edit one file: only it is re-parsed.
	writeFile(t, root, "a.go", "package main\nfunc A(){ _ = 1 }\n")
	edited := run(pipeline.ModeUpdate)
	if edited.Parsed != 1 || edited.Reused != 2 {
		t.Errorf("after editing a.go: parsed=%d reused=%d, want 1/2", edited.Parsed, edited.Reused)
	}

	// Delete a file: it is pruned from the cache.
	if err := os.Remove(filepath.Join(root, "b.go")); err != nil {
		t.Fatal(err)
	}
	deleted := run(pipeline.ModeUpdate)
	if deleted.Total != 2 {
		t.Errorf("after deleting b.go: total = %d, want 2", deleted.Total)
	}
	if deleted.Pruned != 1 {
		t.Errorf("after deleting b.go: pruned = %d, want 1", deleted.Pruned)
	}
}
