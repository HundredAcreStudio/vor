package mcp_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"database/sql"
	"github.com/repowise-dev/repowise-go/internal/analysis/decisions"
	pageModels "github.com/repowise-dev/repowise-go/internal/generation/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/git"
	"github.com/repowise-dev/repowise-go/internal/ingestion/graph"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/decisionstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/gitstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/graphstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/persistence/wikistore"
	"github.com/repowise-dev/repowise-go/internal/providers"
	_ "github.com/repowise-dev/repowise-go/internal/providers/mock"
	mcpserver "github.com/repowise-dev/repowise-go/internal/server/mcp"
)

// synthFixture seeds a DB with a graph (file + symbol), a hotspot, a
// wiki page (so FTS retrieval has material), and one decision record.
// Returns the conn + repo id so each test can build a server with or
// without a provider.
func synthFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	ctx := context.Background()
	tmp := t.TempDir()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(tmp, "wiki.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	r, _ := repos.New(conn).EnsureByLocalPath(ctx, tmp, "synth")

	g := graph.New()
	g.AddFileNode(models.FileInfo{Path: "auth.go", Language: "go"})
	g.AddSymbolNode("auth.go", models.Symbol{
		ID: "auth.go::Login", Name: "Login", Kind: models.KindFunction,
		Visibility: models.VisibilityPublic, Language: "go", StartLine: 10, EndLine: 40,
	})
	g.ComputeMetrics()
	_ = graphstore.New(conn).ReplaceGraph(ctx, r.ID, g)

	owner := git.AuthorShare{Name: "Sarah", Email: "s@x", CommitCount: 9, CommitPct: 1}
	_ = gitstore.New(conn).ReplaceAll(ctx, r.ID, []git.PerFile{
		{Path: "auth.go", CommitCountTotal: 30, CommitCount90d: 12, IsHotspot: true,
			ChurnPercentile: 0.97, PrimaryOwner: &owner, BusFactor: 1, ContributorCount: 1,
			TopAuthors: []git.AuthorShare{owner}},
	})

	ws := wikistore.New(conn)
	_, _ = ws.Upsert(ctx, pageModels.Page{
		RepositoryID: r.ID, PageType: pageModels.PageKindFileOverview,
		Title:      "auth.go — authentication entrypoint",
		Content:    "# auth.go\n\nLogin validates JWT bearer tokens and establishes the session.\n",
		Summary:    "Login validates JWT bearer tokens and establishes the session.",
		TargetPath: "auth.go", SourceHash: "h1",
		ModelName: "mock-1", ProviderName: "mock", Confidence: 1.0,
	})

	_, _ = decisionstore.New(conn).Insert(ctx, r.ID, decisions.Record{
		Title:         "JWT over sessions for auth",
		Decision:      "Use JWT bearer tokens",
		Rationale:     "API must be stateless for k8s horizontal scaling",
		Status:        "active",
		Source:        decisions.SourceADR,
		Confidence:    0.9,
		Verification:  decisions.VerificationExact,
		EvidenceFile:  "auth.go",
		AffectedFiles: []string{"auth.go"},
	})
	return conn, r.ID
}

func synthServer(t *testing.T, conn *sql.DB, repoID string, withProvider bool) *mcpserver.Server {
	t.Helper()
	opts := mcpserver.Options{DB: conn, RepositoryID: repoID}
	if withProvider {
		prov, err := providers.NewProvider("mock", providers.Options{})
		if err != nil {
			t.Fatal(err)
		}
		opts.Provider = prov
	}
	srv, err := mcpserver.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestGetContext_AssemblesCard(t *testing.T) {
	conn, rid := synthFixture(t)
	srv := synthServer(t, conn, rid, false) // no LLM needed for get_context
	text := callTool(t, srv, "repowise_get_context", map[string]any{"target": "auth.go"})

	var out struct {
		Cards []map[string]any `json:"cards"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if len(out.Cards) != 1 {
		t.Fatalf("want 1 card, got %d", len(out.Cards))
	}
	c := out.Cards[0]
	if c["found"] != true {
		t.Errorf("expected found=true: %v", c)
	}
	if !strings.Contains(text, "JWT bearer tokens") {
		t.Errorf("card should carry the wiki summary: %s", text)
	}
	if c["hotspot"] != true {
		t.Errorf("auth.go is a seeded hotspot; card should reflect it: %v", c)
	}
	if !strings.Contains(text, "JWT over sessions for auth") {
		t.Errorf("card should list the decision touching auth.go: %s", text)
	}
	if !strings.Contains(text, "auth.go::Login") {
		t.Errorf("card should list child symbol ids: %s", text)
	}
}

func TestGetContext_MissingTargets(t *testing.T) {
	conn, rid := synthFixture(t)
	srv := synthServer(t, conn, rid, false)
	// callTool fails on tool error; use HandleMessage directly.
	msg := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "repowise_get_context", "arguments": map[string]any{}},
	}
	raw, _ := json.Marshal(msg)
	resp := srv.MCPServer().HandleMessage(context.Background(), raw)
	body, _ := json.Marshal(resp)
	if !strings.Contains(string(body), "isError") {
		t.Errorf("expected error for missing targets: %s", body)
	}
}

func TestGetAnswer_NoProviderReturnsBestGuesses(t *testing.T) {
	conn, rid := synthFixture(t)
	srv := synthServer(t, conn, rid, false)
	text := callTool(t, srv, "repowise_get_answer", map[string]any{
		"question": "how does Login validate tokens",
	})
	if !strings.Contains(text, `"confidence": "none"`) {
		t.Errorf("expected confidence=none without provider: %s", text)
	}
	if !strings.Contains(text, "no LLM provider") {
		t.Errorf("expected the no-provider note: %s", text)
	}
	if !strings.Contains(text, "best_guesses") {
		t.Errorf("expected best_guesses retrieval results: %s", text)
	}
	// FTS over the wiki page should have surfaced auth.go.
	if !strings.Contains(text, "auth.go") {
		t.Errorf("retrieval should have found the auth.go page: %s", text)
	}
}

func TestGetAnswer_WithProviderSynthesises(t *testing.T) {
	conn, rid := synthFixture(t)
	srv := synthServer(t, conn, rid, true)
	text := callTool(t, srv, "repowise_get_answer", map[string]any{
		"question": "how does Login validate tokens",
	})
	var out struct {
		Answer     string `json:"answer"`
		Confidence string `json:"confidence"`
		Citations  []any  `json:"citations"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if out.Answer == "" {
		t.Errorf("expected a synthesised answer from the mock provider: %s", text)
	}
	if len(out.Citations) == 0 {
		t.Errorf("expected citations: %s", text)
	}
	if out.Confidence == "none" {
		t.Errorf("with a provider + a matching page, confidence should not be 'none': %s", text)
	}
}

func TestGetWhy_NoProviderReturnsDecisions(t *testing.T) {
	conn, rid := synthFixture(t)
	srv := synthServer(t, conn, rid, false)
	text := callTool(t, srv, "repowise_get_why", map[string]any{"query": "JWT"})
	if !strings.Contains(text, "no LLM provider") {
		t.Errorf("expected no-provider note: %s", text)
	}
	if !strings.Contains(text, "JWT over sessions for auth") {
		t.Errorf("expected the matched decision: %s", text)
	}
	if !strings.Contains(text, "k8s horizontal scaling") {
		t.Errorf("expected the decision rationale in the raw records: %s", text)
	}
}

func TestGetWhy_ByTarget(t *testing.T) {
	conn, rid := synthFixture(t)
	srv := synthServer(t, conn, rid, false)
	text := callTool(t, srv, "repowise_get_why", map[string]any{
		"targets": []any{"auth.go"},
	})
	if !strings.Contains(text, "JWT over sessions for auth") {
		t.Errorf("target-scoped why should find the auth.go decision: %s", text)
	}
}

func TestGetWhy_WithProviderSynthesises(t *testing.T) {
	conn, rid := synthFixture(t)
	srv := synthServer(t, conn, rid, true)
	text := callTool(t, srv, "repowise_get_why", map[string]any{"query": "JWT"})
	var out struct {
		Rationale string `json:"rationale"`
		Decisions []any  `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if out.Rationale == "" {
		t.Errorf("expected a synthesised rationale: %s", text)
	}
	if len(out.Decisions) == 0 {
		t.Errorf("expected raw decisions attached for verification: %s", text)
	}
}

func TestGetWhy_NoMatchHasHint(t *testing.T) {
	conn, rid := synthFixture(t)
	srv := synthServer(t, conn, rid, false)
	text := callTool(t, srv, "repowise_get_why", map[string]any{"query": "zzznomatch"})
	if !strings.Contains(text, "no architectural decisions matched") {
		t.Errorf("expected no-match hint: %s", text)
	}
}
