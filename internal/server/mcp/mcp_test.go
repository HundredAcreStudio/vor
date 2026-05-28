package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/analysis/deadcode"
	"github.com/HundredAcreStudio/vor/internal/analysis/health"
	pageModels "github.com/HundredAcreStudio/vor/internal/generation/models"
	"github.com/HundredAcreStudio/vor/internal/ingestion/external"
	"github.com/HundredAcreStudio/vor/internal/ingestion/git"
	"github.com/HundredAcreStudio/vor/internal/ingestion/graph"
	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/deadstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/externalstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/gitstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/graphstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/healthstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/persistence/wikistore"
	mcpserver "github.com/HundredAcreStudio/vor/internal/server/mcp"
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

	// wiki pages
	ws := wikistore.New(conn)
	_, _ = ws.Upsert(ctx, pageModels.Page{
		RepositoryID: r.ID, PageType: pageModels.PageKindFileOverview,
		Title: "lib.go overview", Content: "# lib.go\n\nDefines Helper().\n",
		Summary: "Defines Helper().", TargetPath: "lib.go", SourceHash: "hh",
		ModelName: "claude-sonnet-4-6", ProviderName: "anthropic",
		InputTokens: 60, OutputTokens: 20, Confidence: 1.0,
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

func TestVorStatus(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "vor_status", nil)
	for _, want := range []string{`"graphNodes"`, `"deadCodeFindings"`, `"externalsByEcosystem"`, `"npm"`} {
		if !strings.Contains(text, want) {
			t.Errorf("status missing %q in %s", want, text)
		}
	}
}

func TestVorHotspots(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "vor_hotspots", map[string]any{"limit": 5})
	if !strings.Contains(text, "main.go") {
		t.Errorf("hotspots missing main.go: %s", text)
	}
	if !strings.Contains(text, "Alice") {
		t.Errorf("hotspots missing PrimaryOwner: %s", text)
	}
}

func TestVorDeadCode_SafeOnly(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "vor_dead_code", map[string]any{"safe_only": true})
	if !strings.Contains(text, "orphan.go") {
		t.Errorf("dead code missing orphan.go: %s", text)
	}
	if !strings.Contains(text, `"safeToDelete": true`) {
		t.Errorf("expected SafeToDelete=true in payload: %s", text)
	}
}

func TestVorHealth(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "vor_health", nil)
	for _, want := range []string{`"averageScore"`, `"worstFiles"`, "lib.go", `"high_complexity"`} {
		if !strings.Contains(text, want) {
			t.Errorf("health missing %q: %s", want, text)
		}
	}
}

func TestVorHealthFindings_FilterByBiomarker(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "vor_health_findings", map[string]any{
		"biomarker": "high_complexity",
	})
	if !strings.Contains(text, "Helper") {
		t.Errorf("findings missing Helper: %s", text)
	}
	text2 := callTool(t, srv, "vor_health_findings", map[string]any{
		"biomarker": "nope",
	})
	if strings.Contains(text2, "Helper") {
		t.Errorf("filter to unknown biomarker should yield empty findings, got: %s", text2)
	}
}

func TestVorSymbol(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "vor_symbol", map[string]any{
		"symbol_id": "lib.go::Helper",
	})
	for _, want := range []string{`"name": "Helper"`, `"nodeType": "symbol"`, `"filePath": "lib.go"`} {
		if !strings.Contains(text, want) {
			t.Errorf("symbol missing %q in %s", want, text)
		}
	}
}

func TestVorSymbol_NotFound(t *testing.T) {
	srv, _ := fixtureServer(t)
	req := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "vor_symbol",
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

func TestVorDependents(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "vor_dependents", map[string]any{
		"file_path": "lib.go",
	})
	if !strings.Contains(text, "main.go") {
		t.Errorf("dependents missing main.go: %s", text)
	}
}

func TestVorExternals_EcosystemFilter(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "vor_externals", map[string]any{
		"ecosystem": "npm",
	})
	if !strings.Contains(text, "react") {
		t.Errorf("expected react in npm externals: %s", text)
	}
	text2 := callTool(t, srv, "vor_externals", map[string]any{
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

func TestVorPages(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "vor_pages", map[string]any{"limit": 10})
	if !strings.Contains(text, `"lib.go"`) {
		t.Errorf("pages list missing lib.go: %s", text)
	}
	if !strings.Contains(text, `"file_overview"`) {
		t.Errorf("pages list missing pageType: %s", text)
	}
}

func TestVorPage_Found(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "vor_page", map[string]any{"path": "lib.go"})
	if !strings.Contains(text, "Defines Helper") {
		t.Errorf("page body missing markdown: %s", text)
	}
	// The JSON is pretty-printed, so match the field+value flexibly.
	if !strings.Contains(text, `"sourceHash"`) || !strings.Contains(text, `"hh"`) {
		t.Errorf("page payload missing sourceHash: %s", text)
	}
}

func TestVorPage_NotFoundIsToolError(t *testing.T) {
	srv, _ := fixtureServer(t)
	// callTool would fail on tool error; use HandleMessage directly so we
	// can inspect the IsError flag without failing the test.
	msg := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "vor_page", "arguments": map[string]any{"path": "no-such-file"}},
	}
	raw, _ := json.Marshal(msg)
	resp := srv.MCPServer().HandleMessage(context.Background(), raw)
	respBytes, _ := json.Marshal(resp)
	if !strings.Contains(string(respBytes), `"isError":true`) {
		t.Errorf("expected isError=true for missing page: %s", string(respBytes))
	}
}

func TestNew_RejectsMissingRepoID(t *testing.T) {
	var db *sql.DB // intentionally nil to short-circuit before DB use
	_, err := mcpserver.New(mcpserver.Options{DB: db})
	if err == nil {
		t.Errorf("expected error when RepositoryID is empty")
	}
}
