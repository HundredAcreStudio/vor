package registry_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/server/registry"
)

type fakeTracker struct {
	tracked   []string
	untracked []string
}

func (f *fakeTracker) Track(id, root string) { f.tracked = append(f.tracked, id) }
func (f *fakeTracker) Untrack(id string)     { f.untracked = append(f.untracked, id) }

func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(t.TempDir(), "wiki.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestRegister_TracksAndStartsWatch(t *testing.T) {
	conn := freshDB(t)
	tr := &fakeTracker{}
	reg := registry.New(conn, tr, nil)
	ctx := context.Background()
	dir := t.TempDir()

	repo, err := reg.Register(ctx, dir, true)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if repo.Path != dir || !repo.Ephemeral {
		t.Errorf("unexpected repo DTO: %+v", repo)
	}
	if len(tr.tracked) != 1 || tr.tracked[0] != repo.ID {
		t.Errorf("Track not called for %s: %v", repo.ID, tr.tracked)
	}
	// DB reflects tracked + ephemeral.
	row, err := repos.New(conn).Get(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !row.Tracked || !row.Ephemeral {
		t.Errorf("row should be tracked+ephemeral: %+v", row)
	}
	// Listed as tracked.
	if list, _ := reg.ListTracked(ctx); len(list) != 1 {
		t.Errorf("ListTracked = %d, want 1", len(list))
	}
}

func TestUnregister_EphemeralPurges_DurableKeeps(t *testing.T) {
	conn := freshDB(t)
	tr := &fakeTracker{}
	reg := registry.New(conn, tr, nil)
	ctx := context.Background()
	store := repos.New(conn)

	// Ephemeral repo: unregister by path purges the row.
	eph := t.TempDir()
	er, err := reg.Register(ctx, eph, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Unregister(ctx, eph); err != nil {
		t.Fatalf("Unregister ephemeral: %v", err)
	}
	if len(tr.untracked) != 1 || tr.untracked[0] != er.ID {
		t.Errorf("Untrack not called: %v", tr.untracked)
	}
	if _, err := store.Get(ctx, er.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("ephemeral repo should be purged, got err=%v", err)
	}

	// Durable repo: unregister by id keeps the row, just untracks it.
	dur := t.TempDir()
	dr, err := reg.Register(ctx, dur, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Unregister(ctx, dr.ID); err != nil {
		t.Fatalf("Unregister durable: %v", err)
	}
	row, err := store.Get(ctx, dr.ID)
	if err != nil {
		t.Fatalf("durable repo should survive: %v", err)
	}
	if row.Tracked {
		t.Errorf("durable repo should be untracked after unregister")
	}
}

func TestUnregister_UnknownSpec(t *testing.T) {
	conn := freshDB(t)
	reg := registry.New(conn, &fakeTracker{}, nil)
	if _, err := reg.Unregister(context.Background(), "does-not-exist"); err == nil {
		t.Error("expected error for unknown repo spec")
	}
}
