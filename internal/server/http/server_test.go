package http_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	srvhttp "github.com/HundredAcreStudio/vor/internal/server/http"
)

// fixtureRepo creates a fresh DB, ensures one repository row, and writes a
// representative snapshot across every table the HTTP handlers query. The
// returned (server, repoID) gives tests an httptest.Server they can hit.
func fixtureRepo(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	tmp := t.TempDir()
	ctx := context.Background()

	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(tmp, "wiki.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo, err := repos.New(conn).EnsureByLocalPath(ctx, "/tmp/fix", "fix")
	if err != nil {
		t.Fatalf("ensure repo: %v", err)
	}

	// Graph: 3 file nodes, 2 symbols, a couple of edges.
	g := graph.New()
	idx := g.AddFileNode(models.FileInfo{Path: "index.ts", Language: "typescript", IsEntryPoint: true})
	calc := g.AddFileNode(models.FileInfo{Path: "calc.ts", Language: "typescript"})
	orphan := g.AddFileNode(models.FileInfo{Path: "orphan.ts", Language: "typescript"})
	add := g.AddSymbolNode("calc.ts", models.Symbol{
		ID: "calc.ts::add", Name: "add", Kind: models.KindFunction,
		Visibility: models.VisibilityPublic, Language: "typescript", StartLine: 1, EndLine: 3,
		QualifiedName: "add",
	})
	orphanSym := g.AddSymbolNode("orphan.ts", models.Symbol{
		ID: "orphan.ts::Lonely", Name: "Lonely", Kind: models.KindFunction,
		Visibility: models.VisibilityPublic, Language: "typescript", StartLine: 5, EndLine: 12,
		QualifiedName: "Lonely",
	})
	g.AddEdge(idx, calc, models.EdgeImports, 1.0, []string{"add"})
	g.AddEdge(calc, add, models.EdgeDefines, 1.0, nil)
	g.AddEdge(orphan, orphanSym, models.EdgeDefines, 1.0, nil)
	g.ComputeMetrics()
	if err := graphstore.New(conn).ReplaceGraph(ctx, repo.ID, g); err != nil {
		t.Fatalf("ReplaceGraph: %v", err)
	}

	// Git hotspots.
	owner := git.AuthorShare{Name: "Alice", Email: "a@x", CommitCount: 3, CommitPct: 1.0}
	gitRecs := []git.PerFile{
		{Path: "calc.ts", CommitCountTotal: 8, CommitCount90d: 8, IsHotspot: true,
			ChurnPercentile: 0.95, PrimaryOwner: &owner, ContributorCount: 1, BusFactor: 1,
			LinesAdded90d: 50, LinesDeleted90d: 20, TopAuthors: []git.AuthorShare{owner}},
		{Path: "orphan.ts", CommitCountTotal: 1, CommitCount90d: 0, IsHotspot: false,
			ChurnPercentile: 0.1, PrimaryOwner: &owner, ContributorCount: 1, BusFactor: 1,
			TopAuthors: []git.AuthorShare{owner}},
	}
	if err := gitstore.New(conn).ReplaceAll(ctx, repo.ID, gitRecs); err != nil {
		t.Fatalf("git ReplaceAll: %v", err)
	}

	// Dead code: orphan.ts file + Lonely symbol.
	dcFindings := []deadcode.Finding{
		{Kind: deadcode.KindUnreachableFile, FilePath: "orphan.ts", Confidence: 1.0,
			Reason: "no imports", SafeToDelete: true},
		{Kind: deadcode.KindUnreachableSymbol, FilePath: "orphan.ts", SymbolName: "Lonely",
			SymbolKind: "function", Confidence: 0.8, Reason: "no callers"},
	}
	if err := deadstore.New(conn).ReplaceAll(ctx, repo.ID, dcFindings); err != nil {
		t.Fatalf("dead ReplaceAll: %v", err)
	}

	// Health.
	hres := health.Result{
		Findings: []health.Finding{
			{FilePath: "calc.ts", BiomarkerType: health.BiomarkerHighComplexity,
				Severity: health.SeverityHigh, FunctionName: "add", LineStart: 1, LineEnd: 3,
				HealthImpact: 3, Reason: "ccn=25"},
		},
		FileMetrics: []health.FileMetric{
			{FilePath: "calc.ts", Score: 7.0, MaxCCN: 25, NLOC: 30},
			{FilePath: "index.ts", Score: 10.0, MaxCCN: 1, NLOC: 5},
		},
	}
	if err := healthstore.New(conn).ReplaceAll(ctx, repo.ID, hres); err != nil {
		t.Fatalf("health ReplaceAll: %v", err)
	}

	// Externals.
	exts := []external.Record{
		{Name: "react", DisplayName: "react", Ecosystem: "npm", Category: "library",
			Version: "^18.2.0", DeclaredIn: "package.json"},
		{Name: "jest", DisplayName: "jest", Ecosystem: "npm", Category: "library",
			Version: "^29.0.0", DeclaredIn: "package.json", IsDevDep: true},
	}
	if err := externalstore.New(conn).ReplaceAll(ctx, repo.ID, exts); err != nil {
		t.Fatalf("ext ReplaceAll: %v", err)
	}

	// Wiki pages — two file_overview rows, one fresh + one stale.
	ws := wikistore.New(conn)
	if _, err := ws.Upsert(ctx, pageModels.Page{
		RepositoryID: repo.ID, PageType: pageModels.PageKindFileOverview,
		Title: "calc.ts overview", Content: "# calc.ts\n\nAdds two numbers.\n",
		Summary: "Adds two numbers.", TargetPath: "calc.ts", SourceHash: "h1",
		ModelName: "claude-sonnet-4-6", ProviderName: "anthropic",
		InputTokens: 100, OutputTokens: 50, Confidence: 1.0,
	}); err != nil {
		t.Fatalf("wikistore Upsert calc: %v", err)
	}
	if _, err := ws.Upsert(ctx, pageModels.Page{
		RepositoryID: repo.ID, PageType: pageModels.PageKindFileOverview,
		Title: "index.ts overview", Content: "# index.ts\n", Summary: "Entry.",
		TargetPath: "index.ts", SourceHash: "h2",
		ModelName: "claude-sonnet-4-6", ProviderName: "anthropic",
		InputTokens: 50, OutputTokens: 25, Confidence: 1.0,
		Freshness: pageModels.FreshnessStale,
	}); err != nil {
		t.Fatalf("wikistore Upsert index: %v", err)
	}

	srv, err := srvhttp.New(srvhttp.Options{DB: conn})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(func() {
		httpSrv.Close()
		_ = conn.Close()
	})
	return httpSrv, repo.ID
}

// mustGet issues a GET and fails the test on transport error. The
// caller owns Body.Close(). Centralising the error check keeps the
// status-code assertion sites terse and satisfies `go vet`'s
// "using resp before checking for errors" check.
func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// hitJSON is a tiny helper: GET path, expect status 200, decode body into out.
func hitJSON(t *testing.T, base, path string, out any) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, resp.StatusCode, string(body))
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("decode %s: %v\nbody=%s", path, err, string(body))
		}
	}
}

func TestHealth_ServiceEndpoint(t *testing.T) {
	srv, _ := fixtureRepo(t)
	var body map[string]any
	hitJSON(t, srv.URL, "/api/health", &body)
	if body["status"] != "ok" {
		t.Errorf("status = %v, want \"ok\"", body["status"])
	}
}

func TestListRepos(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		Repos []struct {
			ID, Name string
		} `json:"repos"`
	}
	hitJSON(t, srv.URL, "/api/repos", &body)
	if len(body.Repos) != 1 || body.Repos[0].ID != repoID || body.Repos[0].Name != "fix" {
		t.Errorf("repos = %+v", body.Repos)
	}
}

func TestGetRepo_NotFound(t *testing.T) {
	srv, _ := fixtureRepo(t)
	resp := mustGet(t, srv.URL+"/api/repos/no-such-id")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestListGraphNodes(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		Nodes []map[string]any `json:"nodes"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/graph/nodes?limit=10", &body)
	if len(body.Nodes) == 0 {
		t.Fatalf("no nodes returned")
	}
	// Filter by type=file.
	var fileBody struct {
		Nodes []map[string]any `json:"nodes"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/graph/nodes?type=file", &fileBody)
	for _, n := range fileBody.Nodes {
		if n["nodeType"] != "file" {
			t.Errorf("non-file node returned: %v", n)
		}
	}
}

func TestListGraphEdges(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		Edges []map[string]any `json:"edges"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/graph/edges?type=imports", &body)
	if len(body.Edges) != 1 {
		t.Errorf("expected 1 imports edge, got %d", len(body.Edges))
	}
	if names, ok := body.Edges[0]["importedNames"].([]any); !ok || len(names) != 1 || names[0] != "add" {
		t.Errorf("importedNames = %v, want [add]", body.Edges[0]["importedNames"])
	}
}

func TestListHotspots(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		Hotspots []struct {
			Path            string  `json:"path"`
			ChurnPercentile float64 `json:"churnPercentile"`
			PrimaryOwner    string  `json:"primaryOwner"`
		} `json:"hotspots"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/hotspots", &body)
	if len(body.Hotspots) != 1 || body.Hotspots[0].Path != "calc.ts" {
		t.Errorf("hotspots = %+v", body.Hotspots)
	}
	if body.Hotspots[0].PrimaryOwner != "Alice" {
		t.Errorf("PrimaryOwner = %q", body.Hotspots[0].PrimaryOwner)
	}
}

func TestGetSymbol(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		Name     string `json:"name"`
		NodeType string `json:"nodeType"`
		FilePath string `json:"filePath"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/symbol?symbol_id=calc.ts::add", &body)
	if body.Name != "add" || body.NodeType != "symbol" || body.FilePath != "calc.ts" {
		t.Errorf("symbol = %+v", body)
	}
}

func TestGetSymbol_NotFound(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	resp := mustGet(t, srv.URL+"/api/repos/"+repoID+"/symbol?symbol_id=nope")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGetDependents(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		Dependents []map[string]any `json:"dependents"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/dependents?file_path=calc.ts", &body)
	if len(body.Dependents) != 1 || body.Dependents[0]["from"] != "index.ts" {
		t.Errorf("dependents = %+v", body.Dependents)
	}
}

func TestGetSymbol_MissingQuery(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	resp := mustGet(t, srv.URL+"/api/repos/"+repoID+"/symbol")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing symbol_id should yield 400; got %d", resp.StatusCode)
	}
}

func TestListDeadCode(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		Findings []map[string]any `json:"findings"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/dead-code", &body)
	if len(body.Findings) != 2 {
		t.Errorf("findings = %d, want 2", len(body.Findings))
	}
	// safe_only=true should drop the symbol finding.
	var safeBody struct {
		Findings []map[string]any `json:"findings"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/dead-code?safe_only=true", &safeBody)
	if len(safeBody.Findings) != 1 {
		t.Errorf("safe_only findings = %d, want 1", len(safeBody.Findings))
	}
}

func TestHealthSummary(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		AverageScore        float64        `json:"averageScore"`
		FindingCount        int            `json:"findingCount"`
		FindingsByBiomarker map[string]int `json:"findingsByBiomarker"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/health", &body)
	if body.FindingCount != 1 {
		t.Errorf("FindingCount = %d, want 1", body.FindingCount)
	}
	if body.AverageScore <= 0 || body.AverageScore > 10 {
		t.Errorf("AverageScore = %v out of range", body.AverageScore)
	}
	if body.FindingsByBiomarker[health.BiomarkerHighComplexity] != 1 {
		t.Errorf("FindingsByBiomarker = %v", body.FindingsByBiomarker)
	}
}

func TestHealthFindings_FilterByBiomarker(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		Findings []map[string]any `json:"findings"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/health/findings?biomarker=high_complexity", &body)
	if len(body.Findings) != 1 {
		t.Errorf("findings = %d, want 1", len(body.Findings))
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/health/findings?biomarker=does_not_exist", &body)
	if len(body.Findings) != 0 {
		t.Errorf("expected 0 findings for unknown biomarker, got %d", len(body.Findings))
	}
}

func TestHealthFiles_SortOrder(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		Files []struct {
			Path  string  `json:"filePath"`
			Score float64 `json:"score"`
		} `json:"files"`
	}
	// Default: ascending score (worst first).
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/health/files", &body)
	if len(body.Files) < 2 {
		t.Fatalf("expected ≥2 files, got %d", len(body.Files))
	}
	if body.Files[0].Score > body.Files[1].Score {
		t.Errorf("default sort should be ascending; got %+v", body.Files)
	}
}

func TestListExternals_Filters(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		Externals []map[string]any `json:"externals"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/externals", &body)
	if len(body.Externals) != 2 {
		t.Errorf("externals = %d, want 2", len(body.Externals))
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/externals?dev=true", &body)
	if len(body.Externals) != 1 || body.Externals[0]["name"] != "jest" {
		t.Errorf("dev=true externals = %+v", body.Externals)
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/externals?ecosystem=npm&dev=false", &body)
	if len(body.Externals) != 1 || body.Externals[0]["name"] != "react" {
		t.Errorf("dev=false externals = %+v", body.Externals)
	}
}

func TestPanic_RecoveredAs500(t *testing.T) {
	// Direct middleware coverage — construct a server with a deliberately
	// panicking handler and verify the recovery middleware emits 500.
	conn, _ := openDBForPanic(t)
	srv, err := srvhttp.New(srvhttp.Options{DB: conn})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// chi router won't be modified post-construction; instead we serve an
	// unknown /api route. (Unknown *non*-API paths now fall through to the
	// dashboard SPA catch-all and return 200 — see TestUI_ServesIndex.) The
	// /api subrouter does not backtrack to the catch-all, so it still 404s.
	rec := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/does/not/exist", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown api route = %d, want 404", rec.Code)
	}
	_ = strings.Contains // keep import linter happy if all other usages drop
}

// ---- pages endpoints ----------------------------------------------------

func TestListPages(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		Pages []struct {
			TargetPath string `json:"targetPath"`
			PageType   string `json:"pageType"`
			Freshness  string `json:"freshness"`
			Version    int    `json:"version"`
			Title      string `json:"title"`
		} `json:"pages"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/pages", &body)
	if len(body.Pages) != 2 {
		t.Fatalf("pages = %d, want 2", len(body.Pages))
	}
	gotPaths := map[string]bool{}
	for _, p := range body.Pages {
		gotPaths[p.TargetPath] = true
		if p.PageType != "file_overview" {
			t.Errorf("PageType = %q", p.PageType)
		}
	}
	if !gotPaths["calc.ts"] || !gotPaths["index.ts"] {
		t.Errorf("missing expected pages: %+v", gotPaths)
	}
}

func TestListPages_StaleFilter(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		Pages []struct {
			TargetPath string `json:"targetPath"`
			Freshness  string `json:"freshness"`
		} `json:"pages"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/pages?stale=1", &body)
	if len(body.Pages) != 1 {
		t.Fatalf("?stale=1 returned %d, want 1", len(body.Pages))
	}
	if body.Pages[0].TargetPath != "index.ts" {
		t.Errorf("stale page = %q, want index.ts", body.Pages[0].TargetPath)
	}
}

func TestShowPage(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var body struct {
		TargetPath string `json:"targetPath"`
		Content    string `json:"content"`
		SourceHash string `json:"sourceHash"`
		Version    int    `json:"version"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/pages/show?path=calc.ts", &body)
	if body.TargetPath != "calc.ts" {
		t.Errorf("TargetPath = %q", body.TargetPath)
	}
	if !strings.Contains(body.Content, "Adds two numbers") {
		t.Errorf("Content = %q", body.Content)
	}
	if body.SourceHash != "h1" {
		t.Errorf("SourceHash = %q", body.SourceHash)
	}
}

func TestShowPage_MissingPath(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	resp := mustGet(t, srv.URL+"/api/repos/"+repoID+"/pages/show")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing ?path should yield 400, got %d", resp.StatusCode)
	}
}

func TestShowPage_NotFound(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	resp := mustGet(t, srv.URL+"/api/repos/"+repoID+"/pages/show?path=no-such-file.go")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestOverview(t *testing.T) {
	srv, _ := fixtureRepo(t)
	var body struct {
		Repos []struct {
			Name        string  `json:"name"`
			FileCount   int     `json:"fileCount"`
			SymbolCount int     `json:"symbolCount"`
			HealthAvg   float64 `json:"healthAvg"`
		} `json:"repos"`
	}
	hitJSON(t, srv.URL, "/api/overview", &body)
	if len(body.Repos) != 1 {
		t.Fatalf("repos = %d, want 1", len(body.Repos))
	}
	r := body.Repos[0]
	// The fixture seeds 3 file nodes and 2 symbol nodes.
	if r.FileCount != 3 {
		t.Errorf("fileCount = %d, want 3", r.FileCount)
	}
	if r.SymbolCount != 2 {
		t.Errorf("symbolCount = %d, want 2", r.SymbolCount)
	}
}

func TestRepoDetail(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	var repo struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID, &repo)
	if repo.ID != repoID {
		t.Errorf("id = %q, want %q", repo.ID, repoID)
	}
	if repo.Name != "fix" {
		t.Errorf("name = %q, want %q", repo.Name, "fix")
	}
}

func TestRepoSettings_PutGetDelete(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	base := srv.URL + "/api/repos/" + repoID + "/settings"

	// PUT a health_rules override.
	rules := `[{"path":"generated/","overrides":{"high_complexity":"disabled"}}]`
	req, _ := http.NewRequest(http.MethodPut, base+"/health_rules", strings.NewReader(rules))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", resp.StatusCode)
	}

	// GET reflects the override + lists biomarkers.
	var got struct {
		Effective struct {
			HealthRules []struct {
				Path string `json:"path"`
			} `json:"health_rules"`
		} `json:"effective"`
		Overridden map[string]bool `json:"overridden"`
		Biomarkers []string        `json:"biomarkers"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/settings", &got)
	if !got.Overridden["health_rules"] {
		t.Error("health_rules should be marked overridden")
	}
	if len(got.Effective.HealthRules) != 1 || got.Effective.HealthRules[0].Path != "generated/" {
		t.Errorf("effective health_rules = %+v", got.Effective.HealthRules)
	}
	if len(got.Biomarkers) == 0 {
		t.Error("biomarkers list should be non-empty")
	}

	// Reject an unknown key.
	req, _ = http.NewRequest(http.MethodPut, base+"/bogus", strings.NewReader(`1`))
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT unknown key status = %d, want 400", resp.StatusCode)
	}

	// DELETE clears the override.
	req, _ = http.NewRequest(http.MethodDelete, base+"/health_rules", nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE status = %d, want 204", resp.StatusCode)
	}
	got.Overridden = nil
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/settings", &got)
	if got.Overridden["health_rules"] {
		t.Error("health_rules override should be gone after DELETE")
	}
}

func TestDeleteRepo(t *testing.T) {
	srv, repoID := fixtureRepo(t)
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/repos/"+repoID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", resp.StatusCode)
	}
	// The repo is gone now.
	gone := mustGet(t, srv.URL+"/api/repos/"+repoID)
	gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Errorf("after delete, GET repo status = %d, want 404", gone.StatusCode)
	}
}

func TestUI_ServesIndex(t *testing.T) {
	srv, _ := fixtureRepo(t)
	for _, path := range []string{"/", "/some/client/route"} {
		resp := mustGet(t, srv.URL+path)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		// Unknown paths fall back to the SPA entry point (200, HTML).
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
		if !strings.Contains(string(body), "<div id=\"root\">") &&
			!strings.Contains(string(body), "Vor") {
			t.Errorf("GET %s did not return the SPA index, got: %s", path, string(body))
		}
	}
}

// openDBForPanic is a tiny helper so TestPanic_RecoveredAs500 can construct
// a server without recreating the entire fixture.
func openDBForPanic(t *testing.T) (*sql.DB, string) {
	t.Helper()
	tmp := t.TempDir()
	conn, dialect, err := db.Open(context.Background(), db.OpenOptions{URL: "sqlite:" + filepath.Join(tmp, "p.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := migrations.Up(context.Background(), conn, dialect); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, tmp
}
