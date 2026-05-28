package commands_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

// With no daemon running, register/unregister fall back to mutating the
// shared DB directly so the next `vor serve` picks the change up.
func TestRegisterUnregister_DBFallback(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "wiki.db")
	repoDir := t.TempDir()
	env := map[string]string{
		"XDG_STATE_HOME": t.TempDir(), // no daemon.json here -> not live
		"VOR_DB_URL":     "sqlite:" + dbPath,
	}

	// Register ephemeral.
	out, _, err := runVorCmd(t, env, "register", "--ephemeral", repoDir)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !strings.Contains(out, "no daemon running") {
		t.Errorf("expected fallback notice, got: %s", out)
	}

	conn, _, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	store := repos.New(conn)

	tracked, err := store.ListTracked(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != 1 || tracked[0].LocalPath != repoDir || !tracked[0].Ephemeral {
		t.Fatalf("expected one ephemeral tracked repo at %s, got %+v", repoDir, tracked)
	}

	// Unregister -> ephemeral purge: the row disappears.
	if _, _, err := runVorCmd(t, env, "unregister", repoDir); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if got, _ := store.ListTracked(ctx); len(got) != 0 {
		t.Errorf("ListTracked after unregister = %d, want 0", len(got))
	}
}
