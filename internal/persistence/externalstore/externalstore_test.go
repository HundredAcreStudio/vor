package externalstore_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/ingestion/external"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/externalstore"
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

func TestReplaceAll_InsertsAllEcosystems(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := externalstore.New(conn)
	ctx := context.Background()

	recs := []external.Record{
		{Name: "react", DisplayName: "react", Ecosystem: "npm", Category: "library", Version: "^18", DeclaredIn: "package.json"},
		{Name: "jest", DisplayName: "jest", Ecosystem: "npm", Category: "library", Version: "^29", DeclaredIn: "package.json", IsDevDep: true},
		{Name: "requests", DisplayName: "requests", Ecosystem: "pypi", Category: "library", Version: ">=2", DeclaredIn: "pyproject.toml"},
		{Name: "github.com/google/uuid", DisplayName: "uuid", Ecosystem: "go", Category: "library", Version: "v1.5.0", DeclaredIn: "go.mod"},
	}
	if err := store.ReplaceAll(ctx, repoID, recs); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}

	total, err := store.Count(ctx, repoID)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if total != 4 {
		t.Errorf("Count = %d, want 4", total)
	}

	byEco, err := store.CountByEcosystem(ctx, repoID)
	if err != nil {
		t.Fatalf("CountByEcosystem: %v", err)
	}
	if byEco["npm"] != 2 || byEco["pypi"] != 1 || byEco["go"] != 1 {
		t.Errorf("CountByEcosystem = %v", byEco)
	}
}

func TestReplaceAll_OverwritesPreviousSnapshot(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := externalstore.New(conn)
	ctx := context.Background()

	if err := store.ReplaceAll(ctx, repoID, []external.Record{
		{Name: "old1", Ecosystem: "npm", DeclaredIn: "package.json"},
		{Name: "old2", Ecosystem: "npm", DeclaredIn: "package.json"},
	}); err != nil {
		t.Fatalf("first ReplaceAll: %v", err)
	}
	if err := store.ReplaceAll(ctx, repoID, []external.Record{
		{Name: "fresh", Ecosystem: "cargo", DeclaredIn: "Cargo.toml"},
	}); err != nil {
		t.Fatalf("second ReplaceAll: %v", err)
	}
	total, _ := store.Count(ctx, repoID)
	if total != 1 {
		t.Errorf("Count after replace = %d, want 1", total)
	}
}

func TestReplaceAll_CascadesOnRepoDelete(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := externalstore.New(conn)
	ctx := context.Background()

	if err := store.ReplaceAll(ctx, repoID, []external.Record{
		{Name: "x", Ecosystem: "npm", DeclaredIn: "package.json"},
	}); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	if err := repos.New(conn).Delete(ctx, repoID); err != nil {
		t.Fatalf("Delete repo: %v", err)
	}
	n, _ := store.Count(ctx, repoID)
	if n != 0 {
		t.Errorf("Count after cascade delete = %d, want 0", n)
	}
}
