package pipeline_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/pipelinestore"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/pipeline"

	// Side-effect imports for parsers + extractors so Run() has registries.
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/gomod"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/npm"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/golang"
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

func TestRun_AutoGeneratesRunID(t *testing.T) {
	root, conn, repoID := setup(t)
	res, err := pipeline.Run(context.Background(), pipeline.Options{
		RepoPath: root, DB: conn, RepositoryID: repoID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.RunID == "" {
		t.Error("RunID should be auto-generated when not supplied")
	}
	if len(res.RunID) != 32 {
		t.Errorf("RunID = %q, want 32-char hex", res.RunID)
	}
}

func TestRun_HonoursExplicitRunID(t *testing.T) {
	root, conn, repoID := setup(t)
	res, err := pipeline.Run(context.Background(), pipeline.Options{
		RepoPath: root, DB: conn, RepositoryID: repoID,
		RunID: "explicit-run-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.RunID != "explicit-run-id" {
		t.Errorf("RunID = %q, want explicit-run-id", res.RunID)
	}
	// Verify it propagated to pipeline_jobs.metadata_json.
	latest, _ := pipelinestore.New(conn).LatestRun(context.Background(), repoID)
	if latest == nil || latest.RunID != "explicit-run-id" {
		t.Errorf("explicit RunID didn't propagate to pipeline_jobs: %+v", latest)
	}
}

func TestRun_ResumeAdoptsPreviousRunID(t *testing.T) {
	root, conn, repoID := setup(t)
	store := pipelinestore.New(conn)
	ctx := context.Background()

	// Seed a previously-failed run.
	j1, _ := store.Begin(ctx, repoID, "parse", "previous-run")
	_ = store.Start(ctx, j1.ID)
	_ = store.Complete(ctx, j1.ID)
	j2, _ := store.Begin(ctx, repoID, "graph", "previous-run")
	_ = store.Start(ctx, j2.ID)
	_ = store.Fail(ctx, j2.ID, "graph failed")

	// SQLite's CURRENT_TIMESTAMP has 1-second resolution. Sleep so
	// the resume's new rows have a strictly-later updated_at than
	// the seeded failed graph row — otherwise classifyRun's latest-
	// per-phase tiebreaker becomes UUID-string ordering, which is
	// nondeterministic.
	time.Sleep(1100 * time.Millisecond)

	// Resume should pick up that run_id.
	res, err := pipeline.Run(ctx, pipeline.Options{
		RepoPath: root, DB: conn, RepositoryID: repoID,
		Mode: pipeline.ModeResume,
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.RunID != "previous-run" {
		t.Errorf("RunID = %q, want previous-run", res.RunID)
	}
	// The latest pipeline_jobs row should also carry the previous run_id.
	latest, _ := store.LatestRun(ctx, repoID)
	if latest.RunID != "previous-run" {
		t.Errorf("LatestRun.RunID = %q, want previous-run", latest.RunID)
	}
	// And the resume should have succeeded.
	if latest.Overall != pipelinestore.OutcomeSucceeded {
		t.Errorf("after resume, Overall = %q, want succeeded", latest.Overall)
	}
}

func TestRun_ResumeNoPreviousRun(t *testing.T) {
	root, conn, repoID := setup(t)
	_, err := pipeline.Run(context.Background(), pipeline.Options{
		RepoPath: root, DB: conn, RepositoryID: repoID,
		Mode: pipeline.ModeResume,
	})
	if err == nil {
		t.Fatal("expected error when resuming a repo with no previous run")
	}
	if !strings.Contains(err.Error(), "no previous run") {
		t.Errorf("error = %v, want 'no previous run' message", err)
	}
}

func TestRun_ResumeAlreadySucceeded(t *testing.T) {
	root, conn, repoID := setup(t)
	// First run to completion.
	if _, err := pipeline.Run(context.Background(), pipeline.Options{
		RepoPath: root, DB: conn, RepositoryID: repoID,
	}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Resume should refuse — there's nothing to resume.
	_, err := pipeline.Run(context.Background(), pipeline.Options{
		RepoPath: root, DB: conn, RepositoryID: repoID,
		Mode: pipeline.ModeResume,
	})
	if !errors.Is(err, pipeline.ErrNothingToResume) {
		t.Errorf("expected ErrNothingToResume, got %v", err)
	}
}
