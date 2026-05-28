package pipelinestore_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/pipelinestore"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	tmp := t.TempDir()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(tmp, "wiki.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return conn
}

func makeRepo(t *testing.T, conn *sql.DB) string {
	t.Helper()
	r, err := repos.New(conn).EnsureByLocalPath(context.Background(), "/r", "r")
	if err != nil {
		t.Fatalf("ensure repo: %v", err)
	}
	return r.ID
}

func TestBegin_StartComplete_Lifecycle(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := pipelinestore.New(conn)
	ctx := context.Background()

	job, err := store.Begin(ctx, repoID, "parse", "")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if job.State != pipelinestore.StatePending {
		t.Errorf("initial state = %s, want pending", job.State)
	}
	if err := store.Start(ctx, job.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := store.Complete(ctx, job.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	rows, _ := store.LatestByRepo(ctx, repoID, 10)
	if len(rows) != 1 || rows[0].State != pipelinestore.StateCompleted {
		t.Errorf("after Complete: %+v", rows)
	}
}

func TestFail_StampsError(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := pipelinestore.New(conn)
	ctx := context.Background()

	job, _ := store.Begin(ctx, repoID, "graph", "")
	_ = store.Start(ctx, job.ID)
	if err := store.Fail(ctx, job.ID, "boom"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	rows, _ := store.LatestByRepo(ctx, repoID, 10)
	if rows[0].State != pipelinestore.StateFailed {
		t.Errorf("state = %s, want failed", rows[0].State)
	}
	if rows[0].Error != "boom" {
		t.Errorf("error = %q, want boom", rows[0].Error)
	}
}

func TestCountByState(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := pipelinestore.New(conn)
	ctx := context.Background()

	// Three completed, one failed, one running.
	for _, p := range []string{"parse", "graph", "git"} {
		j, _ := store.Begin(ctx, repoID, p, "")
		_ = store.Start(ctx, j.ID)
		_ = store.Complete(ctx, j.ID)
	}
	j1, _ := store.Begin(ctx, repoID, "health", "")
	_ = store.Start(ctx, j1.ID)
	_ = store.Fail(ctx, j1.ID, "x")
	j2, _ := store.Begin(ctx, repoID, "externals", "")
	_ = store.Start(ctx, j2.ID)

	counts, err := store.CountByState(ctx, repoID)
	if err != nil {
		t.Fatalf("CountByState: %v", err)
	}
	if counts[pipelinestore.StateCompleted] != 3 ||
		counts[pipelinestore.StateFailed] != 1 ||
		counts[pipelinestore.StateRunning] != 1 {
		t.Errorf("counts = %+v", counts)
	}
}

func TestBegin_RequiresInputs(t *testing.T) {
	store := pipelinestore.New(freshDB(t))
	if _, err := store.Begin(context.Background(), "", "parse", ""); err == nil {
		t.Errorf("expected error on empty repoID")
	}
	if _, err := store.Begin(context.Background(), "x", "", ""); err == nil {
		t.Errorf("expected error on empty phase")
	}
}

func TestLatestByRepo_Ordering(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := pipelinestore.New(conn)
	ctx := context.Background()

	// Insert in a known order with seconds between rows; LatestByRepo
	// sorts DESC by started_at, and SQLite's CURRENT_TIMESTAMP has
	// 1-second resolution.
	phases := []string{"alpha", "beta", "gamma"}
	for i, p := range phases {
		if i > 0 {
			time.Sleep(1100 * time.Millisecond)
		}
		_, _ = store.Begin(ctx, repoID, p, "")
	}
	rows, _ := store.LatestByRepo(ctx, repoID, 10)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	// Most recent first; the gamma insert is the latest.
	if rows[0].Phase != "gamma" {
		t.Errorf("rows[0].Phase = %q, want gamma", rows[0].Phase)
	}
}
