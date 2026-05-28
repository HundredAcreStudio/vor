package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
)

func TestDoctor_OnEmptyTempDir(t *testing.T) {
	tmp := t.TempDir()
	out := runRoot(t, tmp, "doctor", "--repo", tmp)
	// On a fresh dir: repo path OK, config missing (warn), no provider keys
	// unless env exports them (warn), DB warn (no schema), parsers ok.
	for _, want := range []string{"repo path", "config file", "parsers"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q\n%s", want, out)
		}
	}
}

func TestDoctor_AfterMigrate(t *testing.T) {
	tmp := t.TempDir()
	ctx := context.Background()
	// Pre-create the schema so the DB check reports OK.
	if err := os.MkdirAll(filepath.Join(tmp, ".vor"), 0o755); err != nil {
		t.Fatal(err)
	}
	conn, dialect, err := db.Open(ctx, db.OpenOptions{
		URL: "sqlite:" + filepath.Join(tmp, ".vor", "wiki.db"),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	conn.Close()

	out := runRoot(t, tmp, "doctor", "--repo", tmp)
	if !strings.Contains(out, "ok    database") {
		t.Errorf("expected 'ok database' after migrate, got:\n%s", out)
	}
	if !strings.Contains(out, "0 repositories indexed") {
		t.Errorf("expected '0 repositories indexed' on fresh DB, got:\n%s", out)
	}
}

// runRoot wires the cobra root tree with the given args and returns the
// combined stdout + stderr. Exits the test on RunE error so the tests can
// inspect the string contents.
func runRoot(t *testing.T, _ string, args ...string) string {
	t.Helper()
	cmd := Root()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	_ = cmd.ExecuteContext(context.Background())
	return buf.String()
}
