package repos_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

// freshDB opens a fresh SQLite database, applies migrations, and registers
// a cleanup. Returns the *sql.DB the test should use.
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

func TestEnsureByLocalPath_CreatesNewRow(t *testing.T) {
	conn := freshDB(t)
	store := repos.New(conn)
	ctx := context.Background()

	r, err := store.EnsureByLocalPath(ctx, "/path/to/myrepo", "")
	if err != nil {
		t.Fatalf("EnsureByLocalPath: %v", err)
	}
	if r.ID == "" {
		t.Errorf("ID should be set, got empty")
	}
	if r.Name != "myrepo" {
		t.Errorf("Name = %q, want \"myrepo\" (inferred from path)", r.Name)
	}
	if r.LocalPath != "/path/to/myrepo" {
		t.Errorf("LocalPath = %q", r.LocalPath)
	}
	if r.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want \"main\"", r.DefaultBranch)
	}
	if r.SettingsJSON != "{}" {
		t.Errorf("SettingsJSON = %q, want \"{}\"", r.SettingsJSON)
	}
}

func TestEnsureByLocalPath_ReturnsExisting(t *testing.T) {
	conn := freshDB(t)
	store := repos.New(conn)
	ctx := context.Background()

	r1, err := store.EnsureByLocalPath(ctx, "/path", "first")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	r2, err := store.EnsureByLocalPath(ctx, "/path", "second-name-should-be-ignored")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if r1.ID != r2.ID {
		t.Errorf("IDs differ: %s vs %s — should be same row", r1.ID, r2.ID)
	}
	if r2.Name != "first" {
		t.Errorf("Name = %q, want \"first\" (existing row should win)", r2.Name)
	}
}

func TestUpdateHeadCommit(t *testing.T) {
	conn := freshDB(t)
	store := repos.New(conn)
	ctx := context.Background()

	r, _ := store.EnsureByLocalPath(ctx, "/r", "r")
	if err := store.UpdateHeadCommit(ctx, r.ID, "abc123"); err != nil {
		t.Fatalf("UpdateHeadCommit: %v", err)
	}
	got, _ := store.Get(ctx, r.ID)
	if got.HeadCommit != "abc123" {
		t.Errorf("HeadCommit = %q, want abc123", got.HeadCommit)
	}
}

func TestList(t *testing.T) {
	conn := freshDB(t)
	store := repos.New(conn)
	ctx := context.Background()

	_, _ = store.EnsureByLocalPath(ctx, "/a", "alpha")
	_, _ = store.EnsureByLocalPath(ctx, "/b", "beta")
	_, _ = store.EnsureByLocalPath(ctx, "/c", "gamma")

	all, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("List returned %d rows, want 3", len(all))
	}
	// ORDER BY name → alpha, beta, gamma
	if all[0].Name != "alpha" || all[1].Name != "beta" || all[2].Name != "gamma" {
		t.Errorf("List order = %v %v %v, want alpha/beta/gamma",
			all[0].Name, all[1].Name, all[2].Name)
	}
}

func TestDelete(t *testing.T) {
	conn := freshDB(t)
	store := repos.New(conn)
	ctx := context.Background()

	r, _ := store.EnsureByLocalPath(ctx, "/r", "r")
	if err := store.Delete(ctx, r.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := store.Get(ctx, r.ID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Get after Delete: err = %v, want sql.ErrNoRows", err)
	}
}

func TestSetTracked_AndListTracked(t *testing.T) {
	conn := freshDB(t)
	store := repos.New(conn)
	ctx := context.Background()

	a, err := store.EnsureByLocalPath(ctx, "/repo/a", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.EnsureByLocalPath(ctx, "/repo/b", "")
	if err != nil {
		t.Fatal(err)
	}

	// New repos are untracked by default and absent from ListTracked.
	if a.Tracked {
		t.Errorf("new repo should be untracked")
	}
	if got, _ := store.ListTracked(ctx); len(got) != 0 {
		t.Errorf("ListTracked on fresh DB = %d, want 0", len(got))
	}

	// Track a (ephemeral), leave b untracked.
	if err := store.SetTracked(ctx, a.ID, true, true); err != nil {
		t.Fatal(err)
	}
	tracked, err := store.ListTracked(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != 1 || tracked[0].ID != a.ID {
		t.Fatalf("ListTracked = %+v, want only %s", tracked, a.ID)
	}
	if !tracked[0].Ephemeral || !tracked[0].TrackedAt.Valid {
		t.Errorf("tracked repo should be ephemeral with tracked_at set: %+v", tracked[0])
	}

	// Untrack a; ListTracked empties again.
	if err := store.SetTracked(ctx, a.ID, false, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.ListTracked(ctx); len(got) != 0 {
		t.Errorf("ListTracked after untrack = %d, want 0", len(got))
	}
	_ = b
}
