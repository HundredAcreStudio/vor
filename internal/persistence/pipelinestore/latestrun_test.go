package pipelinestore_test

import (
	"context"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/persistence/pipelinestore"
)

func TestLatestRun_NoRuns(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	got, err := pipelinestore.New(conn).LatestRun(context.Background(), repoID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for empty repo, got %+v", got)
	}
}

func TestLatestRun_GroupsByRunID(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := pipelinestore.New(conn)
	ctx := context.Background()

	// Two runs: run-A with 3 phases (all completed), run-B with
	// 2 phases (one completed, one failed).
	for _, p := range []string{"parse", "graph", "persist"} {
		j, _ := store.Begin(ctx, repoID, p, "run-A")
		_ = store.Start(ctx, j.ID)
		_ = store.Complete(ctx, j.ID)
	}
	for _, p := range []string{"parse", "graph"} {
		j, _ := store.Begin(ctx, repoID, p, "run-B")
		_ = store.Start(ctx, j.ID)
		if p == "graph" {
			_ = store.Fail(ctx, j.ID, "boom")
		} else {
			_ = store.Complete(ctx, j.ID)
		}
	}

	latest, err := store.LatestRun(ctx, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil {
		t.Fatal("expected a run")
	}
	if latest.RunID != "run-B" {
		t.Errorf("RunID = %q, want run-B (most recent)", latest.RunID)
	}
	if latest.Overall != pipelinestore.OutcomeFailed {
		t.Errorf("Overall = %q, want failed", latest.Overall)
	}
	if len(latest.Phases) != 2 {
		t.Errorf("Phases = %d, want 2", len(latest.Phases))
	}
}

func TestLatestRun_OutcomeSucceeded(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := pipelinestore.New(conn)
	ctx := context.Background()

	for _, p := range []string{"parse", "graph", "persist"} {
		j, _ := store.Begin(ctx, repoID, p, "run-X")
		_ = store.Start(ctx, j.ID)
		_ = store.Complete(ctx, j.ID)
	}
	latest, _ := store.LatestRun(ctx, repoID)
	if latest.Overall != pipelinestore.OutcomeSucceeded {
		t.Errorf("Overall = %q, want succeeded", latest.Overall)
	}
}

func TestLatestRun_OutcomeRunning(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := pipelinestore.New(conn)
	ctx := context.Background()

	j1, _ := store.Begin(ctx, repoID, "parse", "run-R")
	_ = store.Start(ctx, j1.ID)
	_ = store.Complete(ctx, j1.ID)
	j2, _ := store.Begin(ctx, repoID, "graph", "run-R")
	_ = store.Start(ctx, j2.ID)
	// graph left in state=running, never completed

	latest, _ := store.LatestRun(ctx, repoID)
	if latest.Overall != pipelinestore.OutcomeRunning {
		t.Errorf("Overall = %q, want running", latest.Overall)
	}
}

func TestLatestRun_IgnoresRowsWithoutRunID(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := pipelinestore.New(conn)
	ctx := context.Background()

	// Legacy: no run_id.
	j1, _ := store.Begin(ctx, repoID, "parse", "")
	_ = store.Complete(ctx, j1.ID)
	// New: run_id set.
	j2, _ := store.Begin(ctx, repoID, "graph", "run-N")
	_ = store.Complete(ctx, j2.ID)

	latest, _ := store.LatestRun(ctx, repoID)
	if latest == nil || latest.RunID != "run-N" {
		t.Errorf("LatestRun should ignore legacy rows: %+v", latest)
	}
	if len(latest.Phases) != 1 {
		t.Errorf("Phases = %d, want 1 (just the run-N row)", len(latest.Phases))
	}
}
