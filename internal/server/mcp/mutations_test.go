package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/pipelinestore"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	mcpserver "github.com/repowise-dev/repowise-go/internal/server/mcp"

	// Register the Go parser so a reindex does real work in this test
	// binary (resolvers + decision sources come in via the pipeline import).
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/golang"
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

func TestReindex_AsyncRunCompletes(t *testing.T) {
	srv, conn, repoID, _ := mutationFixture(t, map[string]string{
		"main.go": "package main\nfunc main(){ helper() }\nfunc helper(){}\n",
		"go.mod":  "module example.com/x\ngo 1.21\n",
	})

	text := callTool(t, srv, "repowise_reindex", map[string]any{"mode": "update"})
	var out struct {
		Status string `json:"status"`
		RunID  string `json:"runId"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if out.Status != "started" || out.RunID == "" {
		t.Fatalf("expected started + run_id, got %s", text)
	}

	// Poll until the run finishes (it is tiny, so this is quick).
	store := pipelinestore.New(conn)
	deadline := time.Now().Add(10 * time.Second)
	var overall string
	for time.Now().Before(deadline) {
		run, err := store.LatestRun(context.Background(), repoID)
		if err == nil && run != nil && run.Overall != pipelinestore.OutcomeRunning {
			overall = run.Overall
			if run.RunID != out.RunID {
				t.Errorf("latest run id = %s, want %s", run.RunID, out.RunID)
			}
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if overall != pipelinestore.OutcomeSucceeded {
		t.Fatalf("run did not succeed in time (overall=%q)", overall)
	}

	// pipeline_log should now report the completed phases.
	logText := callTool(t, srv, "repowise_pipeline_log", map[string]any{"limit": 20})
	if !strings.Contains(logText, "completed") || !strings.Contains(logText, "parse") {
		t.Errorf("pipeline_log missing completed phases: %s", logText)
	}
}

func TestReindex_DoesNotDoubleFire(t *testing.T) {
	srv, conn, repoID, _ := mutationFixture(t, map[string]string{
		"main.go": "package main\nfunc main(){}\n", "go.mod": "module x\ngo 1.21\n",
	})
	// Seed an in-progress run directly so the guard has something to find.
	store := pipelinestore.New(conn)
	job, err := store.Begin(context.Background(), repoID, "parse", "stuck-run")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Start(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}

	text := callTool(t, srv, "repowise_reindex", nil)
	var out struct {
		Status string `json:"status"`
		RunID  string `json:"runId"`
	}
	_ = json.Unmarshal([]byte(text), &out)
	if out.Status != "already_running" {
		t.Errorf("expected already_running, got %s", text)
	}
	if out.RunID != "stuck-run" {
		t.Errorf("expected the in-progress run id, got %s", out.RunID)
	}
}

func TestSecurityScan_StoresAndReadsBack(t *testing.T) {
	srv, _, _, _ := mutationFixture(t, map[string]string{
		"app.py": "API_KEY = \"supersecret12345\"\nimport hashlib\nh = hashlib.md5(b\"x\")\n",
		"go.mod": "module x\ngo 1.21\n",
	})

	text := callTool(t, srv, "repowise_security_scan", nil)
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

	// repowise_security should read the stored findings back.
	readBack := callTool(t, srv, "repowise_security", map[string]any{"limit": 50})
	if !strings.Contains(readBack, "hardcoded_secret") {
		t.Errorf("read-back missing hardcoded_secret: %s", readBack)
	}
	// The secret value must never be persisted verbatim.
	if strings.Contains(readBack, "supersecret12345") {
		t.Errorf("secret leaked into stored snippet: %s", readBack)
	}
}
