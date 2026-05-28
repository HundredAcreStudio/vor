package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	mcpserver "github.com/HundredAcreStudio/vor/internal/server/mcp"

	// Register the Go parser so the security scan walks real source in
	// this test binary.
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/golang"
)

// mutationFixture builds a server over a real on-disk repo dir so the
// mutating tools have files to act on.
func mutationFixture(t *testing.T, files map[string]string) (*mcpserver.Server, *sql.DB, string, string) {
	t.Helper()
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
	// Source lives in its own dir so the wiki.db isn't part of the repo.
	src := filepath.Join(tmp, "src")
	for rel, body := range files {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := repos.New(conn).EnsureByLocalPath(ctx, src, "mut")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := mcpserver.New(mcpserver.Options{DB: conn, RepositoryID: r.ID})
	if err != nil {
		t.Fatal(err)
	}
	return srv, conn, r.ID, src
}

func TestSecurityScan_StoresAndReadsBack(t *testing.T) {
	srv, _, _, _ := mutationFixture(t, map[string]string{
		"app.py": "API_KEY = \"supersecret12345\"\nimport hashlib\nh = hashlib.md5(b\"x\")\n",
		"go.mod": "module x\ngo 1.21\n",
	})

	text := callTool(t, srv, "vor_security_scan", nil)
	var scan struct {
		Findings   int            `json:"findings"`
		BySeverity map[string]int `json:"bySeverity"`
	}
	if err := json.Unmarshal([]byte(text), &scan); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if scan.Findings < 2 {
		t.Fatalf("expected >=2 findings (secret + weak hash), got %d: %s", scan.Findings, text)
	}

	// vor_security should read the stored findings back.
	readBack := callTool(t, srv, "vor_security", map[string]any{"limit": 50})
	if !strings.Contains(readBack, "hardcoded_secret") {
		t.Errorf("read-back missing hardcoded_secret: %s", readBack)
	}
	// The secret value must never be persisted verbatim.
	if strings.Contains(readBack, "supersecret12345") {
		t.Errorf("secret leaked into stored snippet: %s", readBack)
	}
}
