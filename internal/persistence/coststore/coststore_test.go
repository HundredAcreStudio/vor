package coststore_test

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/persistence/coststore"
	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/providers"
)

func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	tmp := t.TempDir()
	url := "sqlite:" + filepath.Join(tmp, "wiki.db")
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: url})
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

func TestInsertAndAggregate(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := coststore.New(conn)
	ctx := context.Background()

	entries := []coststore.Entry{
		{RepositoryID: repoID, Model: "claude-3-5-sonnet", Operation: "page",
			Usage: providers.Usage{InputTokens: 500, OutputTokens: 200}, CostUSD: 0.005},
		{RepositoryID: repoID, Model: "claude-3-5-sonnet", Operation: "page",
			Usage: providers.Usage{InputTokens: 800, OutputTokens: 300}, CostUSD: 0.007},
		{RepositoryID: repoID, Model: "claude-3-5-haiku", Operation: "answer",
			Usage: providers.Usage{InputTokens: 200, OutputTokens: 80}, CostUSD: 0.001, FilePath: "src/foo.go"},
	}
	for _, e := range entries {
		if err := store.Insert(ctx, e); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	if n, _ := store.Count(ctx, repoID); n != 3 {
		t.Errorf("Count = %d, want 3", n)
	}

	total, err := store.TotalUSD(ctx, repoID)
	if err != nil {
		t.Fatalf("TotalUSD: %v", err)
	}
	if math.Abs(total-0.013) > 1e-9 {
		t.Errorf("TotalUSD = %v, want 0.013", total)
	}

	byOp, err := store.TotalByOperation(ctx, repoID)
	if err != nil {
		t.Fatalf("TotalByOperation: %v", err)
	}
	if math.Abs(byOp["page"]-0.012) > 1e-9 {
		t.Errorf("page total = %v, want 0.012", byOp["page"])
	}
	if math.Abs(byOp["answer"]-0.001) > 1e-9 {
		t.Errorf("answer total = %v, want 0.001", byOp["answer"])
	}
}

func TestInsert_RequiresRepoID(t *testing.T) {
	conn := freshDB(t)
	store := coststore.New(conn)
	err := store.Insert(context.Background(), coststore.Entry{Model: "x", Operation: "y"})
	if err == nil {
		t.Errorf("expected error when RepositoryID is empty")
	}
}

func TestTotalUSD_EmptyRepoIsZero(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	total, err := coststore.New(conn).TotalUSD(context.Background(), repoID)
	if err != nil {
		t.Fatalf("TotalUSD: %v", err)
	}
	if total != 0 {
		t.Errorf("empty repo total = %v, want 0", total)
	}
}

func TestCount_CascadesOnRepoDelete(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := coststore.New(conn)
	ctx := context.Background()
	_ = store.Insert(ctx, coststore.Entry{
		RepositoryID: repoID, Model: "m", Operation: "o",
		Usage: providers.Usage{InputTokens: 1, OutputTokens: 1}, CostUSD: 0.001,
	})
	if err := repos.New(conn).Delete(ctx, repoID); err != nil {
		t.Fatalf("delete repo: %v", err)
	}
	if n, _ := store.Count(ctx, repoID); n != 0 {
		t.Errorf("Count after delete = %d, want 0", n)
	}
}
