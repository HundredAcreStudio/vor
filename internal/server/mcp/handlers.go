package mcp

import (
	"context"
	"fmt"
	"sort"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/HundredAcreStudio/vor/internal/persistence/externalstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/graphstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/healthstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/vector"
	"github.com/HundredAcreStudio/vor/internal/workspace"
)

// ---- tool: vor_workspace_repos --------------------------------------

type workspaceRepoEntry struct {
	Alias         string `json:"alias"`
	Path          string `json:"path"`
	RepositoryID  string `json:"repository_id"`
	WorkspaceRoot string `json:"workspace_root,omitempty"`
	IsDefault     bool   `json:"is_default"`
}

func (s *Server) toolWorkspaceRepos(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	roots := s.opts.workspaceRoots()
	if len(roots) == 0 {
		return jsonResult(map[string]any{
			"repos":   []workspaceRepoEntry{},
			"message": "server is not running in workspace mode (no workspace root configured)",
		})
	}
	// Union members across every registered workspace. The
	// workspace_root field disambiguates when the same alias appears
	// in two workspaces.
	out := []workspaceRepoEntry{}
	for _, root := range roots {
		state, err := workspace.Load(root)
		if err != nil {
			continue
		}
		for _, e := range state.Sorted() {
			entry := workspaceRepoEntry{
				Alias:         e.Alias,
				Path:          e.Path,
				WorkspaceRoot: root,
				IsDefault:     e.Alias == state.DefaultAlias,
			}
			if id, err := s.repoIDForPath(ctx, e.Path); err == nil {
				entry.RepositoryID = id
			}
			out = append(out, entry)
		}
	}
	return jsonResult(map[string]any{"repos": out})
}

// ---- tool: vor_status -----------------------------------------------

type statusPayload struct {
	GraphNodes       int            `json:"graphNodes"`
	GraphEdges       int            `json:"graphEdges"`
	HotspotFiles     int            `json:"hotspotFiles"`
	DeadCodeFindings int            `json:"deadCodeFindings"`
	AvgHealthScore   float64        `json:"avgHealthScore"`
	HealthFindings   int            `json:"healthFindings"`
	ExternalsByEco   map[string]int `json:"externalsByEcosystem"`
}

func (s *Server) toolStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	p := statusPayload{}

	gs := graphstore.New(s.opts.DB)
	if n, err := gs.CountNodes(ctx, rid); err == nil {
		p.GraphNodes = n
	}
	if counts, err := gs.CountByEdgeType(ctx, rid); err == nil {
		for _, c := range counts {
			p.GraphEdges += c
		}
	}

	_ = s.opts.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM git_metadata WHERE repository_id = ? AND is_hotspot = 1`,
		rid).Scan(&p.HotspotFiles)
	_ = s.opts.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dead_code_findings WHERE repository_id = ?`,
		rid).Scan(&p.DeadCodeFindings)

	hs := healthstore.New(s.opts.DB)
	if v, err := hs.AverageScore(ctx, rid); err == nil {
		p.AvgHealthScore = v
	}
	if v, err := hs.CountFindings(ctx, rid); err == nil {
		p.HealthFindings = v
	}

	es := externalstore.New(s.opts.DB)
	if v, err := es.CountByEcosystem(ctx, rid); err == nil {
		p.ExternalsByEco = v
	}

	return jsonResult(p)
}

// ---- tool: vor_hotspots ---------------------------------------------

type hotspotPayload struct {
	Path             string  `json:"path"`
	ChurnPercentile  float64 `json:"churnPercentile"`
	CommitCountTotal int     `json:"commitCountTotal"`
	PrimaryOwner     string  `json:"primaryOwner,omitempty"`
	BusFactor        int     `json:"busFactor"`
}

func (s *Server) toolHotspots(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := clampInt(req.GetInt("limit", 20), 1, 100)
	rows, err := s.opts.DB.QueryContext(ctx,
		`SELECT file_path, churn_percentile, commit_count_total,
		        COALESCE(primary_owner_name, ''), bus_factor
		 FROM git_metadata
		 WHERE repository_id = ? AND is_hotspot = 1
		 ORDER BY churn_percentile DESC, file_path
		 LIMIT ?`, rid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]hotspotPayload, 0, limit)
	for rows.Next() {
		var h hotspotPayload
		if err := rows.Scan(&h.Path, &h.ChurnPercentile, &h.CommitCountTotal,
			&h.PrimaryOwner, &h.BusFactor); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return jsonResult(map[string]any{"hotspots": out, "limit": limit})
}

// ---- tool: vor_dead_code --------------------------------------------

type deadPayload struct {
	Kind         string  `json:"kind"`
	FilePath     string  `json:"filePath"`
	SymbolName   string  `json:"symbolName,omitempty"`
	SymbolKind   string  `json:"symbolKind,omitempty"`
	Confidence   float64 `json:"confidence"`
	Reason       string  `json:"reason"`
	SafeToDelete bool    `json:"safeToDelete"`
}

func (s *Server) toolDeadCode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := clampInt(req.GetInt("limit", 50), 1, 500)
	safeOnly := req.GetBool("safe_only", false)

	query := `SELECT kind, file_path, COALESCE(symbol_name,''), COALESCE(symbol_kind,''),
	                 confidence, reason, safe_to_delete
	          FROM dead_code_findings WHERE repository_id = ?`
	args := []any{rid}
	if safeOnly {
		query += " AND safe_to_delete = 1"
	}
	query += " ORDER BY confidence DESC, file_path LIMIT ?"
	args = append(args, limit)

	rows, err := s.opts.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]deadPayload, 0, limit)
	for rows.Next() {
		var d deadPayload
		var safe int
		if err := rows.Scan(&d.Kind, &d.FilePath, &d.SymbolName, &d.SymbolKind,
			&d.Confidence, &d.Reason, &safe); err != nil {
			return nil, err
		}
		d.SafeToDelete = safe == 1
		out = append(out, d)
	}
	return jsonResult(map[string]any{"findings": out, "limit": limit})
}

// ---- tool: vor_health -----------------------------------------------

type healthSummaryPayload struct {
	AverageScore        float64           `json:"averageScore"`
	FindingCount        int               `json:"findingCount"`
	FindingsByBiomarker map[string]int    `json:"findingsByBiomarker"`
	WorstFiles          []healthFileEntry `json:"worstFiles"`
}

type healthFileEntry struct {
	Path   string  `json:"path"`
	Score  float64 `json:"score"`
	MaxCCN int     `json:"maxCcn"`
}

func (s *Server) toolHealth(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	worstLimit := clampInt(req.GetInt("worst_limit", 10), 1, 50)
	hs := healthstore.New(s.opts.DB)
	p := healthSummaryPayload{}
	p.AverageScore, _ = hs.AverageScore(ctx, rid)
	p.FindingCount, _ = hs.CountFindings(ctx, rid)
	p.FindingsByBiomarker, _ = hs.CountByBiomarker(ctx, rid)

	rows, err := s.opts.DB.QueryContext(ctx,
		`SELECT file_path, score, max_ccn FROM health_file_metrics
		 WHERE repository_id = ?
		 ORDER BY score ASC LIMIT ?`, rid, worstLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e healthFileEntry
		if err := rows.Scan(&e.Path, &e.Score, &e.MaxCCN); err != nil {
			return nil, err
		}
		p.WorstFiles = append(p.WorstFiles, e)
	}
	// Stable order for tests / clients comparing snapshots.
	sort.Slice(p.WorstFiles, func(i, j int) bool {
		if p.WorstFiles[i].Score != p.WorstFiles[j].Score {
			return p.WorstFiles[i].Score < p.WorstFiles[j].Score
		}
		return p.WorstFiles[i].Path < p.WorstFiles[j].Path
	})

	return jsonResult(p)
}

// ---- tool: vor_health_findings --------------------------------------

type healthFindingPayload struct {
	FilePath      string  `json:"filePath"`
	BiomarkerType string  `json:"biomarkerType"`
	Severity      string  `json:"severity"`
	FunctionName  string  `json:"functionName,omitempty"`
	LineStart     int     `json:"lineStart,omitempty"`
	LineEnd       int     `json:"lineEnd,omitempty"`
	HealthImpact  float64 `json:"healthImpact"`
	Reason        string  `json:"reason"`
}

func (s *Server) toolHealthFindings(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := clampInt(req.GetInt("limit", 50), 1, 500)
	biomarker := req.GetString("biomarker", "")

	query := `SELECT file_path, biomarker_type, severity, COALESCE(function_name,''),
	                 COALESCE(line_start,0), COALESCE(line_end,0), health_impact, reason
	          FROM health_findings WHERE repository_id = ?`
	args := []any{rid}
	if biomarker != "" {
		query += " AND biomarker_type = ?"
		args = append(args, biomarker)
	}
	query += ` ORDER BY CASE severity
	                     WHEN 'critical' THEN 0
	                     WHEN 'high'     THEN 1
	                     WHEN 'medium'   THEN 2
	                     ELSE 3 END, health_impact DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.opts.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]healthFindingPayload, 0, limit)
	for rows.Next() {
		var f healthFindingPayload
		if err := rows.Scan(&f.FilePath, &f.BiomarkerType, &f.Severity, &f.FunctionName,
			&f.LineStart, &f.LineEnd, &f.HealthImpact, &f.Reason); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return jsonResult(map[string]any{"findings": out, "limit": limit})
}

// ---- tool: vor_security ---------------------------------------------

type securityPayload struct {
	FilePath string `json:"filePath"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Line     int    `json:"line,omitempty"`
	Snippet  string `json:"snippet,omitempty"`
}

func (s *Server) toolSecurity(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := clampInt(req.GetInt("limit", 100), 1, 500)
	severity := req.GetString("severity", "")

	query := `SELECT file_path, kind, severity, COALESCE(line_number,0), COALESCE(snippet,'')
	          FROM security_findings WHERE repository_id = ?`
	args := []any{rid}
	if severity != "" {
		query += " AND severity = ?"
		args = append(args, severity)
	}
	query += ` ORDER BY CASE severity
	                     WHEN 'critical' THEN 0
	                     WHEN 'high'     THEN 1
	                     WHEN 'medium'   THEN 2
	                     ELSE 3 END, file_path, line_number LIMIT ?`
	args = append(args, limit)

	rows, err := s.opts.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]securityPayload, 0, limit)
	for rows.Next() {
		var f securityPayload
		if err := rows.Scan(&f.FilePath, &f.Kind, &f.Severity, &f.Line, &f.Snippet); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return jsonResult(map[string]any{"findings": out, "limit": limit})
}

// ---- tool: vor_symbol -----------------------------------------------

type symbolPayload struct {
	NodeID        string  `json:"nodeId"`
	NodeType      string  `json:"nodeType"`
	Kind          string  `json:"kind,omitempty"`
	Name          string  `json:"name,omitempty"`
	QualifiedName string  `json:"qualifiedName,omitempty"`
	FilePath      string  `json:"filePath,omitempty"`
	StartLine     int     `json:"startLine,omitempty"`
	EndLine       int     `json:"endLine,omitempty"`
	Visibility    string  `json:"visibility,omitempty"`
	Signature     string  `json:"signature,omitempty"`
	PageRank      float64 `json:"pagerank"`
	Language      string  `json:"language,omitempty"`
}

func (s *Server) toolSymbol(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	id, err := req.RequireString("symbol_id")
	if err != nil {
		return nil, err
	}
	row := s.opts.DB.QueryRowContext(ctx,
		`SELECT node_id, node_type, COALESCE(kind,''), COALESCE(name,''),
		        COALESCE(qualified_name,''), COALESCE(file_path,''),
		        COALESCE(start_line,0), COALESCE(end_line,0),
		        COALESCE(visibility,''), COALESCE(signature,''),
		        pagerank, language
		 FROM graph_nodes WHERE repository_id = ? AND node_id = ?`,
		rid, id)
	var p symbolPayload
	if err := row.Scan(&p.NodeID, &p.NodeType, &p.Kind, &p.Name, &p.QualifiedName,
		&p.FilePath, &p.StartLine, &p.EndLine, &p.Visibility, &p.Signature,
		&p.PageRank, &p.Language); err != nil {
		return mcp.NewToolResultError("symbol not found: " + id), nil
	}
	return jsonResult(p)
}

// ---- tool: vor_callers ----------------------------------------------

type callerEdgePayload struct {
	From       string  `json:"from"`
	EdgeType   string  `json:"edgeType"`
	Confidence float64 `json:"confidence"`
}

func (s *Server) toolCallers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	target, err := req.RequireString("symbol_id")
	if err != nil {
		return nil, err
	}
	limit := clampInt(req.GetInt("limit", 50), 1, 500)
	rows, err := s.opts.DB.QueryContext(ctx,
		`SELECT source_node_id, edge_type, confidence
		 FROM graph_edges
		 WHERE repository_id = ? AND target_node_id = ?
		   AND edge_type IN ('calls', 'has_method', 'method_overrides', 'method_implements')
		 ORDER BY confidence DESC, source_node_id
		 LIMIT ?`, rid, target, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]callerEdgePayload, 0, limit)
	for rows.Next() {
		var e callerEdgePayload
		if err := rows.Scan(&e.From, &e.EdgeType, &e.Confidence); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return jsonResult(map[string]any{
		"symbolId": target,
		"callers":  out,
	})
}

// ---- tool: vor_dependents -------------------------------------------

type dependentEdgePayload struct {
	From          string   `json:"from"`
	Confidence    float64  `json:"confidence"`
	ImportedNames []string `json:"importedNames,omitempty"`
}

func (s *Server) toolDependents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	target, err := req.RequireString("file_path")
	if err != nil {
		return nil, err
	}
	limit := clampInt(req.GetInt("limit", 50), 1, 500)
	rows, err := s.opts.DB.QueryContext(ctx,
		`SELECT source_node_id, confidence, imported_names_json
		 FROM graph_edges
		 WHERE repository_id = ? AND target_node_id = ? AND edge_type = 'imports'
		 ORDER BY source_node_id LIMIT ?`,
		rid, target, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]dependentEdgePayload, 0, limit)
	for rows.Next() {
		var (
			d         dependentEdgePayload
			namesJSON string
		)
		if err := rows.Scan(&d.From, &d.Confidence, &namesJSON); err != nil {
			return nil, err
		}
		if namesJSON != "" && namesJSON != "[]" {
			d.ImportedNames = unmarshalStringArray(namesJSON)
		}
		out = append(out, d)
	}
	return jsonResult(map[string]any{
		"filePath":   target,
		"dependents": out,
	})
}

// ---- tool: vor_externals --------------------------------------------

type externalPayload struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Ecosystem   string `json:"ecosystem"`
	Version     string `json:"version,omitempty"`
	DeclaredIn  string `json:"declaredIn"`
	IsDevDep    bool   `json:"isDevDep"`
}

func (s *Server) toolExternals(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := clampInt(req.GetInt("limit", 200), 1, 1000)
	ecosystem := req.GetString("ecosystem", "")
	devOnly := req.GetBool("dev_only", false)

	query := `SELECT name, display_name, ecosystem, COALESCE(version,''), declared_in, is_dev_dep
	          FROM external_systems WHERE repository_id = ?`
	args := []any{rid}
	if ecosystem != "" {
		query += " AND ecosystem = ?"
		args = append(args, ecosystem)
	}
	if devOnly {
		query += " AND is_dev_dep = 1"
	}
	query += " ORDER BY ecosystem, name LIMIT ?"
	args = append(args, limit)

	rows, err := s.opts.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]externalPayload, 0, limit)
	for rows.Next() {
		var e externalPayload
		var dev int
		if err := rows.Scan(&e.Name, &e.DisplayName, &e.Ecosystem, &e.Version, &e.DeclaredIn, &dev); err != nil {
			return nil, err
		}
		e.IsDevDep = dev == 1
		out = append(out, e)
	}
	return jsonResult(map[string]any{
		"externals": out,
		"limit":     limit,
	})
}

// ---- tool: vor_search -----------------------------------------------

type searchResultPayload struct {
	NodeID    string  `json:"nodeId"`
	NodeType  string  `json:"nodeType"`
	Kind      string  `json:"kind,omitempty"`
	Name      string  `json:"name,omitempty"`
	FilePath  string  `json:"filePath,omitempty"`
	StartLine int     `json:"startLine,omitempty"`
	PageRank  float64 `json:"pagerank"`
}

func (s *Server) toolSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	q, err := req.RequireString("query")
	if err != nil {
		return nil, err
	}
	limit := clampInt(req.GetInt("limit", 25), 1, 200)
	nodeType := req.GetString("node_type", "")

	// Semantic mode: embed the query and rank wiki pages by cosine
	// similarity. Falls through to the lexical path below when no
	// embedder is configured or the repo has no embeddings yet.
	if req.GetBool("semantic", false) {
		if res, ok, err := s.semanticSearch(ctx, rid, q, limit); err != nil {
			return nil, err
		} else if ok {
			return res, nil
		}
	}

	pattern := "%" + q + "%"
	sqlQ := `SELECT node_id, node_type, COALESCE(kind,''), COALESCE(name,''),
	                COALESCE(file_path,''), COALESCE(start_line,0), pagerank
	         FROM graph_nodes
	         WHERE repository_id = ?
	           AND (name LIKE ? OR qualified_name LIKE ? OR node_id LIKE ?)`
	args := []any{rid, pattern, pattern, pattern}
	if nodeType != "" {
		sqlQ += " AND node_type = ?"
		args = append(args, nodeType)
	}
	sqlQ += " ORDER BY pagerank DESC, node_id LIMIT ?"
	args = append(args, limit)

	rows, err := s.opts.DB.QueryContext(ctx, sqlQ, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]searchResultPayload, 0, limit)
	for rows.Next() {
		var r searchResultPayload
		if err := rows.Scan(&r.NodeID, &r.NodeType, &r.Kind, &r.Name, &r.FilePath, &r.StartLine, &r.PageRank); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return jsonResult(map[string]any{
		"query":   q,
		"matches": out,
		"limit":   limit,
	})
}

type semanticMatch struct {
	TargetPath string  `json:"targetPath"`
	Title      string  `json:"title,omitempty"`
	Score      float64 `json:"score"`
}

// semanticSearch embeds the query and ranks wiki pages by cosine
// similarity via the vector store. Returns ok=false (not an error)
// when semantic search isn't possible — no embedder configured, or no
// embeddings stored for the repo — so the caller falls back to lexical.
func (s *Server) semanticSearch(ctx context.Context, rid, q string, limit int) (*mcp.CallToolResult, bool, error) {
	if s.opts.Embedder == nil {
		return nil, false, nil
	}
	vstore := vector.New(s.opts.DB)
	if n, err := vstore.Count(ctx, rid); err != nil || n == 0 {
		return nil, false, nil
	}
	vecs, err := s.opts.Embedder.Embed(ctx, []string{q})
	if err != nil || len(vecs) != 1 {
		return nil, false, nil
	}
	matches, err := vstore.Search(ctx, rid, vecs[0], vector.KindPage, limit)
	if err != nil {
		return nil, false, err
	}
	out := make([]semanticMatch, 0, len(matches))
	for _, m := range matches {
		sm := semanticMatch{TargetPath: m.TargetPath, Score: m.Score}
		_ = s.opts.DB.QueryRowContext(ctx,
			`SELECT COALESCE(title,'') FROM wiki_pages WHERE repository_id = ? AND target_path = ?`,
			rid, m.TargetPath).Scan(&sm.Title)
		out = append(out, sm)
	}
	res, err := jsonResult(map[string]any{
		"query":    q,
		"semantic": true,
		"matches":  out,
		"limit":    limit,
	})
	return res, true, err
}

// ---- tool: vor_pipeline_log ------------------------------------------

type pipelineLogEntry struct {
	Phase     string `json:"phase"`
	State     string `json:"state"`
	StartedAt string `json:"startedAt"`
	UpdatedAt string `json:"updatedAt"`
	Error     string `json:"error,omitempty"`
}

func (s *Server) toolPipelineLog(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := clampInt(req.GetInt("limit", 20), 1, 200)
	rows, err := s.opts.DB.QueryContext(ctx,
		`SELECT phase, state, started_at, updated_at, COALESCE(error,'')
		 FROM pipeline_jobs WHERE repository_id = ?
		 ORDER BY started_at DESC, id DESC LIMIT ?`,
		rid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]pipelineLogEntry, 0, limit)
	for rows.Next() {
		var (
			e                    pipelineLogEntry
			startedAt, updatedAt string
		)
		if err := rows.Scan(&e.Phase, &e.State, &startedAt, &updatedAt, &e.Error); err != nil {
			return nil, err
		}
		e.StartedAt = startedAt
		e.UpdatedAt = updatedAt
		out = append(out, e)
	}
	return jsonResult(map[string]any{
		"entries": out,
		"limit":   limit,
	})
}

// ---- tool: vor_decisions ---------------------------------------------

type decisionPayload struct {
	Title        string  `json:"title"`
	Source       string  `json:"source"`
	EvidenceFile string  `json:"evidenceFile,omitempty"`
	EvidenceLine int     `json:"evidenceLine,omitempty"`
	Confidence   float64 `json:"confidence"`
	Verification string  `json:"verification"`
	Decision     string  `json:"decision,omitempty"`
	Rationale    string  `json:"rationale,omitempty"`
	SourceQuote  string  `json:"sourceQuote,omitempty"`
}

func (s *Server) toolDecisions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := clampInt(req.GetInt("limit", 50), 1, 500)
	source := req.GetString("source", "")

	query := `SELECT dr.title, dr.source, COALESCE(dr.evidence_file,''),
	                 COALESCE(dr.evidence_line,0), dr.confidence, dr.verification,
	                 dr.decision, dr.rationale,
	                 COALESCE((SELECT source_quote FROM decision_evidence de
	                           WHERE de.decision_id = dr.id LIMIT 1), '')
	          FROM decision_records dr
	          WHERE dr.repository_id = ?`
	args := []any{rid}
	if source != "" {
		query += " AND dr.source = ?"
		args = append(args, source)
	}
	query += " ORDER BY dr.confidence DESC, dr.evidence_file, dr.evidence_line LIMIT ?"
	args = append(args, limit)

	rows, err := s.opts.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]decisionPayload, 0, limit)
	for rows.Next() {
		var d decisionPayload
		if err := rows.Scan(&d.Title, &d.Source, &d.EvidenceFile, &d.EvidenceLine,
			&d.Confidence, &d.Verification, &d.Decision, &d.Rationale, &d.SourceQuote); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return jsonResult(map[string]any{"decisions": out, "limit": limit})
}

// ---- tool: vor_pages / vor_page --------------------------------

type pageSummaryPayload struct {
	ID           string `json:"id"`
	PageType     string `json:"pageType"`
	TargetPath   string `json:"targetPath"`
	Title        string `json:"title"`
	Summary      string `json:"summary,omitempty"`
	Version      int    `json:"version"`
	Freshness    string `json:"freshness"`
	ModelName    string `json:"modelName"`
	ProviderName string `json:"providerName"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
	CachedTokens int    `json:"cachedTokens"`
}

type pageContentPayload struct {
	pageSummaryPayload
	Content    string `json:"content"`
	SourceHash string `json:"sourceHash"`
}

func (s *Server) toolPages(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := clampInt(req.GetInt("limit", 100), 1, 500)
	kind := req.GetString("kind", "")
	staleOnly := req.GetBool("stale_only", false)

	query := `SELECT id, page_type, target_path, title, summary,
	                 version, freshness_status, model_name, provider_name,
	                 input_tokens, output_tokens, cached_tokens
	          FROM wiki_pages WHERE repository_id = ?`
	args := []any{rid}
	if kind != "" {
		query += " AND page_type = ?"
		args = append(args, kind)
	}
	if staleOnly {
		query += " AND freshness_status != 'fresh'"
	}
	query += " ORDER BY target_path LIMIT ?"
	args = append(args, limit)

	rows, err := s.opts.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]pageSummaryPayload, 0, limit)
	for rows.Next() {
		var p pageSummaryPayload
		if err := rows.Scan(&p.ID, &p.PageType, &p.TargetPath, &p.Title, &p.Summary,
			&p.Version, &p.Freshness, &p.ModelName, &p.ProviderName,
			&p.InputTokens, &p.OutputTokens, &p.CachedTokens); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return jsonResult(map[string]any{"pages": out, "limit": limit})
}

func (s *Server) toolPage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	target := req.GetString("path", "")
	if target == "" {
		return mcp.NewToolResultError("missing required argument 'path'"), nil
	}
	kind := req.GetString("kind", "file_overview")

	var p pageContentPayload
	err = s.opts.DB.QueryRowContext(ctx, `
		SELECT id, page_type, target_path, title, summary,
		       version, freshness_status, model_name, provider_name,
		       input_tokens, output_tokens, cached_tokens,
		       content, source_hash
		FROM wiki_pages
		WHERE repository_id = ? AND page_type = ? AND target_path = ?
	`, rid, kind, target).Scan(
		&p.ID, &p.PageType, &p.TargetPath, &p.Title, &p.Summary,
		&p.Version, &p.Freshness, &p.ModelName, &p.ProviderName,
		&p.InputTokens, &p.OutputTokens, &p.CachedTokens,
		&p.Content, &p.SourceHash,
	)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("page not found: %s (%s)", target, kind)), nil
	}
	return jsonResult(p)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func unmarshalStringArray(s string) []string {
	var out []string
	if err := jsonUnmarshalLocal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}
