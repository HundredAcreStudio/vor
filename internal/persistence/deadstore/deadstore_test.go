package deadstore_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/analysis/deadcode"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/deadstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
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

func TestReplaceAll_AndAggregates(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := deadstore.New(conn)
	ctx := context.Background()

	findings := []deadcode.Finding{
		{Kind: deadcode.KindUnreachableFile, FilePath: "orphan.go",
			Confidence: 1.0, Reason: "no incoming imports", SafeToDelete: true},
		{Kind: deadcode.KindUnreachableSymbol, FilePath: "orphan.go",
			SymbolName: "Unused", SymbolKind: "function",
			Confidence: 0.8, Reason: "no callers", SafeToDelete: false},
		{Kind: deadcode.KindUnreachableSymbol, FilePath: "orphan.go",
			SymbolName: "Used", SymbolKind: "function",
			Confidence: 0.5, Reason: "referenced only by dead caller", SafeToDelete: false},
	}
	if err := store.ReplaceAll(ctx, repoID, findings); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	if n, _ := store.Count(ctx, repoID); n != 3 {
		t.Errorf("Count = %d, want 3", n)
	}

	safe, err := store.SafeToDelete(ctx, repoID, 10)
	if err != nil {
		t.Fatalf("SafeToDelete: %v", err)
	}
	if len(safe) != 1 || safe[0].FilePath != "orphan.go" {
		t.Errorf("SafeToDelete = %+v, want one orphan.go file row", safe)
	}
}

func TestReplaceAll_OverwritesPrevious(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := deadstore.New(conn)
	ctx := context.Background()

	_ = store.ReplaceAll(ctx, repoID, []deadcode.Finding{
		{Kind: deadcode.KindUnreachableFile, FilePath: "a.go", Confidence: 0.9, SafeToDelete: true},
		{Kind: deadcode.KindUnreachableFile, FilePath: "b.go", Confidence: 0.9, SafeToDelete: true},
	})
	_ = store.ReplaceAll(ctx, repoID, []deadcode.Finding{
		{Kind: deadcode.KindUnreachableFile, FilePath: "c.go", Confidence: 0.9, SafeToDelete: true},
	})
	if n, _ := store.Count(ctx, repoID); n != 1 {
		t.Errorf("Count after replace = %d, want 1", n)
	}
}

func TestCount_CascadeOnRepoDelete(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := deadstore.New(conn)
	ctx := context.Background()
	_ = store.ReplaceAll(ctx, repoID, []deadcode.Finding{
		{Kind: deadcode.KindUnreachableFile, FilePath: "x.go", Confidence: 0.5},
	})
	if err := repos.New(conn).Delete(ctx, repoID); err != nil {
		t.Fatalf("delete repo: %v", err)
	}
	if n, _ := store.Count(ctx, repoID); n != 0 {
		t.Errorf("Count after cascade delete = %d, want 0", n)
	}
}
