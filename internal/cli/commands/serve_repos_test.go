package commands

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
)

func TestRegisterGlobalRepos(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(tmp, "wiki.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}

	a := filepath.Join(tmp, "a")
	b := filepath.Join(tmp, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	missing := filepath.Join(tmp, "nope")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Includes a duplicate (a) and a non-existent path (missing) — both filtered.
	ids, err := registerGlobalRepos(ctx, conn, []string{a, b, a, missing}, logger)
	if err != nil {
		t.Fatalf("registerGlobalRepos: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("got %d ids, want 2 (dedup + skip-missing): %v", len(ids), ids)
	}
	if ids[0] == ids[1] {
		t.Errorf("ids should be distinct: %v", ids)
	}

	// Idempotent: registering the same set again yields the same first id.
	again, err := registerGlobalRepos(ctx, conn, []string{a, b}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if again[0] != ids[0] {
		t.Errorf("re-register changed id: %s -> %s", ids[0], again[0])
	}
}

func TestExpandUser(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := map[string]string{
		"~":          home,
		"~/projects": filepath.Join(home, "projects"),
		"/abs/path":  "/abs/path",
		"relative":   "relative",
		"~notme/x":   "~notme/x", // only ~ and ~/ expand
	}
	for in, want := range cases {
		if got := expandUser(in); got != want {
			t.Errorf("expandUser(%q) = %q, want %q", in, got, want)
		}
	}
}
