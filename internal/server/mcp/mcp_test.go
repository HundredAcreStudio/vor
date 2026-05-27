package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/deadcode"
	"github.com/repowise-dev/repowise-go/internal/analysis/health"
	"github.com/repowise-dev/repowise-go/internal/ingestion/external"
	"github.com/repowise-dev/repowise-go/internal/ingestion/git"
	"github.com/repowise-dev/repowise-go/internal/ingestion/graph"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/deadstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/externalstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/gitstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/graphstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/healthstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	mcpserver "github.com/repowise-dev/repowise-go/internal/server/mcp"
)

// fixtureServer builds a DB with one repo + a representative snapshot
// across every table the MCP tools read, then returns the MCP server +
// repo ID. Mirrors the HTTP server's fixture.
func fixtureServer(t *testing.T) (*mcpserver.Server, string) {
	t.Helper()
	tmp := t.TempDir()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{
		URL: "sqlite:" + filepath.Join(tmp, "wiki.db"),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r, _ := repos.New(conn).EnsureByLocalPath(ctx, "/tmp/mcp-test", "mcp-test")

	// graph
	g := graph.New()
	main := g.AddFileNode(models.FileInfo{Path: "main.go", Language: "go", IsEntryPoint: true})
	lib := g.AddFileNode(models.FileInfo{Path: "lib.go", Language: "go"})
	sym := g.AddSymbolNode("lib.go", models.Symbol{
		ID: "lib.go::Helper", Name: "Helper", Kind: models.KindFunction,
		Visibility: models.VisibilityPublic, Language: "go",
	})
	g.AddEdge(main, lib, models.EdgeImports, 1.0, nil)
	g.AddEdge(lib, sym, models.EdgeDefines, 1.0, nil)
	g.ComputeMetrics()
	_ = graphstore.New(conn).ReplaceGraph(ctx, r.ID, g)

	// git
	owner := git.AuthorShare{Name: "Alice", Email: "a@x", CommitCount: 5, CommitPct: 1.0}
	_ = gitstore.New(conn).ReplaceAll(ctx, r.ID, []git.PerFile{
		{Path: "main.go", CommitCountTotal: 10, CommitCount90d: 10, IsHotspot: true,
			ChurnPercentile: 0.95, PrimaryOwner: &owner, BusFactor: 1, ContributorCount: 1,
			TopAuthors: []git.AuthorShare{owner}},
	})
	// dead code
	_ = deadstore.New(conn).ReplaceAll(ctx, r.ID, []deadcode.Finding{
		{Kind: deadcode.KindUnreachableFile, FilePath: "orphan.go", Confidence: 1.0,
			Reason: "no imports", SafeToDelete: true},
	})
	// health
	_ = healthstore.New(conn).ReplaceAll(ctx, r.ID, health.Result{
		Findings: []health.Finding{
			{FilePath: "lib.go", BiomarkerType: health.BiomarkerHighComplexity,
				Severity: health.SeverityHigh, FunctionName: "Helper",
				LineStart: 1, LineEnd: 50, HealthImpact: 3, Reason: "ccn=25"},
		},
		FileMetrics: []health.FileMetric{
			{FilePath: "lib.go", Score: 6.0, MaxCCN: 25, NLOC: 50},
			{FilePath: "main.go", Score: 10.0, MaxCCN: 1, NLOC: 10},
		},
	})
	// externals
	_ = externalstore.New(conn).ReplaceAll(ctx, r.ID, []external.Record{
		{Name: "react", Ecosystem: "npm", Category: "library",
			Version: "^18", DeclaredIn: "package.json"},
	})

	srv, err := mcpserver.New(mcpserver.Options{DB: conn, RepositoryID: r.ID})
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return srv, r.ID
}

// callTool sends a JSON-RPC tools/call message via HandleMessage and
// returns the text payload of the first content block.
func callTool(t *testing.T, s *mcpserver.Server, name string, args map[string]any) string {
	t.Helper()
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
	raw, _ := json.Marshal(msg)
	resp := s.MCPServer().HandleMessage(context.Background(), raw)
	if resp == nil {
		t.Fatalf("%s: nil response", name)
	}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("%s: marshal response: %v", name, err)
	}
	// Result.content[0].text is the canonical JSON payload.
	var parsed struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		t.Fatalf("%s: unmarshal response: %v\n%s", name, err, string(respBytes))
	}
	if parsed.Error.Message != "" {
		t.Fatalf("%s: rpc error: %s", name, parsed.Error.Message)
	}
	if parsed.Result.IsError {
		t.Fatalf("%s: tool error: %s", name, parsed.Result.Content[0].Text)
	}
	if len(parsed.Result.Content) == 0 {
		t.Fatalf("%s: empty content", name)
	}
	return parsed.Result.Content[0].Text
}

func TestRepowiseStatus(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_status", nil)
	for _, want := range []string{`"graphNodes"`, `"deadCodeFindings"`, `"externalsByEcosystem"`, `"npm"`} {
		if !strings.Contains(text, want) {
			t.Errorf("status missing %q in %s", want, text)
		}
	}
}

func TestRepowiseHotspots(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_hotspots", map[string]any{"limit": 5})
	if !strings.Contains(text, "main.go") {
		t.Errorf("hotspots missing main.go: %s", text)
	}
	if !strings.Contains(text, "Alice") {
		t.Errorf("hotspots missing PrimaryOwner: %s", text)
	}
}

func TestRepowiseDeadCode_SafeOnly(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_dead_code", map[string]any{"safe_only": true})
	if !strings.Contains(text, "orphan.go") {
		t.Errorf("dead code missing orphan.go: %s", text)
	}
	if !strings.Contains(text, `"safeToDelete": true`) {
		t.Errorf("expected SafeToDelete=true in payload: %s", text)
	}
}

func TestRepowiseHealth(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_health", nil)
	for _, want := range []string{`"averageScore"`, `"worstFiles"`, "lib.go", `"high_complexity"`} {
		if !strings.Contains(text, want) {
			t.Errorf("health missing %q: %s", want, text)
		}
	}
}

func TestRepowiseHealthFindings_FilterByBiomarker(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_health_findings", map[string]any{
		"biomarker": "high_complexity",
	})
	if !strings.Contains(text, "Helper") {
		t.Errorf("findings missing Helper: %s", text)
	}
	text2 := callTool(t, srv, "repowise_health_findings", map[string]any{
		"biomarker": "nope",
	})
	if strings.Contains(text2, "Helper") {
		t.Errorf("filter to unknown biomarker should yield empty findings, got: %s", text2)
	}
}

func TestRepowiseSymbol(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_symbol", map[string]any{
		"symbol_id": "lib.go::Helper",
	})
	for _, want := range []string{`"name": "Helper"`, `"nodeType": "symbol"`, `"filePath": "lib.go"`} {
		if !strings.Contains(text, want) {
			t.Errorf("symbol missing %q in %s", want, text)
		}
	}
}

func TestRepowiseSymbol_NotFound(t *testing.T) {
	srv, _ := fixtureServer(t)
	req := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "repowise_symbol",
			"arguments": map[string]any{"symbol_id": "does/not/exist::Foo"},
		},
	}
	raw, _ := json.Marshal(req)
	resp := srv.MCPServer().HandleMessage(context.Background(), raw)
	respBytes, _ := json.Marshal(resp)
	if !strings.Contains(string(respBytes), "not found") {
		t.Errorf("expected 'not found' response, got: %s", string(respBytes))
	}
}

func TestRepowiseDependents(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_dependents", map[string]any{
		"file_path": "lib.go",
	})
	if !strings.Contains(text, "main.go") {
		t.Errorf("dependents missing main.go: %s", text)
	}
}

func TestRepowiseExternals_EcosystemFilter(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_externals", map[string]any{
		"ecosystem": "npm",
	})
	if !strings.Contains(text, "react") {
		t.Errorf("expected react in npm externals: %s", text)
	}
	text2 := callTool(t, srv, "repowise_externals", map[string]any{
		"ecosystem": "cargo",
	})
	if strings.Contains(text2, "react") {
		t.Errorf("cargo filter should exclude npm react: %s", text2)
	}
}

func TestNew_RejectsMissingDB(t *testing.T) {
	_, err := mcpserver.New(mcpserver.Options{RepositoryID: "x"})
	if err == nil {
		t.Errorf("expected error when DB is nil")
	}
}

func TestNew_RejectsMissingRepoID(t *testing.T) {
	var db *sql.DB // intentionally nil to short-circuit before DB use
	_, err := mcpserver.New(mcpserver.Options{DB: db})
	if err == nil {
		t.Errorf("expected error when RepositoryID is empty")
	}
}
