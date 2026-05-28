package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// writeClaudeMd must not rewrite the file when the only difference is the
// volatile "Last indexed" timestamp — so a committed CLAUDE.md doesn't
// churn on every re-index — but must rewrite on a real content change.
func TestWriteClaudeMd_IdempotentOnTimestamp(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "CLAUDE.md")

	if err := writeClaudeMd(dest, "Last indexed: 2020-01-01 00:00:00Z\n\nbody\n"); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(dest)

	// Only the timestamp differs → no-op.
	if err := writeClaudeMd(dest, "Last indexed: 2099-12-31 23:59:59Z\n\nbody\n"); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(dest)
	if string(first) != string(second) {
		t.Errorf("timestamp-only change rewrote the file:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}

	// A real content change must be written.
	if err := writeClaudeMd(dest, "Last indexed: 2099-12-31 23:59:59Z\n\nNEW body\n"); err != nil {
		t.Fatal(err)
	}
	third, _ := os.ReadFile(dest)
	if string(third) == string(second) {
		t.Error("a real content change should have rewritten the file")
	}
}
