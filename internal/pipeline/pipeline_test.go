package pipeline_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/pipelinestore"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/pipeline"

	// Side-effect imports for parsers + extractors so Run() has registries.
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/gomod"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/npm"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/golang"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (string, *sql.DB, string) {
	t.Helper()
	tmp := t.TempDir()
	ctx := context.Background()

	conn, dialect, err := db.Open(ctx, db.OpenOptions{
		URL: "sqlite:" + filepath.Join(tmp, "wiki.db"),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r, err := repos.New(conn).EnsureByLocalPath(ctx, tmp, "pipeline-test")
	if err != nil {
		t.Fatalf("ensure repo: %v", err)
	}

	// Seed a tiny Go file + manifest so the pipeline has something to do.
	writeFile(t, tmp, "main.go", "package main\nfunc main(){}\n")
	writeFile(t, tmp, "go.mod", "module example.com/x\ngo 1.21\nrequire github.com/google/uuid v1.5.0\n")
	t.Cleanup(func() { _ = conn.Close() })
	return tmp, conn, r.ID
}

func TestRun_FullPipeline(t *testing.T) {
	root, conn, repoID := setup(t)
	ctx := context.Background()

	res, err := pipeline.Run(ctx, pipeline.Options{
		RepoPath:     root,
		Mode:         pipeline.ModeInit,
		DB:           conn,
		RepositoryID: repoID,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Phases) != 9 {
		t.Errorf("Phases = %d, want 9", len(res.Phases))
	}
	for _, p := range res.Phases {
		if p.State != pipelinestore.StateCompleted {
			t.Errorf("phase %s state = %s, want completed", p.Phase, p.State)
		}
	}
	if res.Graph == nil || res.Graph.NodeCount() == 0 {
		t.Errorf("graph empty after pipeline run")
	}
	if len(res.Externals) == 0 {
		t.Errorf("expected one external (uuid) from go.mod, got 0")
	}

	// pipeline_jobs should have one row per phase.
	store := pipelinestore.New(conn)
	counts, _ := store.CountByState(ctx, repoID)
	if counts[pipelinestore.StateCompleted] != 9 {
		t.Errorf("completed rows = %d, want 9 (%+v)", counts[pipelinestore.StateCompleted], counts)
	}
}

func TestRun_RejectsMissingInputs(t *testing.T) {
	_, err := pipeline.Run(context.Background(), pipeline.Options{})
	if err == nil {
		t.Errorf("expected error on empty Options")
	}
}

func TestRun_DefaultsModeToInit(t *testing.T) {
	root, conn, repoID := setup(t)
	res, err := pipeline.Run(context.Background(), pipeline.Options{
		RepoPath: root, DB: conn, RepositoryID: repoID,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Phases) == 0 {
		t.Errorf("no phases recorded")
	}
}
