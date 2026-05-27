package mcp

import (
	"context"
	"sort"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/repowise-dev/repowise-go/internal/persistence/externalstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/graphstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/healthstore"
)

// ---- tool: repowise_status -----------------------------------------------

type statusPayload struct {
	GraphNodes       int            `json:"graphNodes"`
	GraphEdges       int            `json:"graphEdges"`
	HotspotFiles     int            `json:"hotspotFiles"`
	DeadCodeFindings int            `json:"deadCodeFindings"`
	AvgHealthScore   float64        `json:"avgHealthScore"`
	HealthFindings   int            `json:"healthFindings"`
	ExternalsByEco   map[string]int `json:"externalsByEcosystem"`
}

func (s *Server) toolStatus(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p := statusPayload{}

	gs := graphstore.New(s.opts.DB)
	if n, err := gs.CountNodes(ctx, s.opts.RepositoryID); err == nil {
		p.GraphNodes = n
	}
	if counts, err := gs.CountByEdgeType(ctx, s.opts.RepositoryID); err == nil {
		for _, c := range counts {
			p.GraphEdges += c
		}
	}

	_ = s.opts.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM git_metadata WHERE repository_id = ? AND is_hotspot = 1`,
		s.opts.RepositoryID).Scan(&p.HotspotFiles)
	_ = s.opts.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dead_code_findings WHERE repository_id = ?`,
		s.opts.RepositoryID).Scan(&p.DeadCodeFindings)

	hs := healthstore.New(s.opts.DB)
	if v, err := hs.AverageScore(ctx, s.opts.RepositoryID); err == nil {
		p.AvgHealthScore = v
	}
	if v, err := hs.CountFindings(ctx, s.opts.RepositoryID); err == nil {
		p.HealthFindings = v
	}

	es := externalstore.New(s.opts.DB)
	if v, err := es.CountByEcosystem(ctx, s.opts.RepositoryID); err == nil {
		p.ExternalsByEco = v
	}

	return jsonResult(p)
}

// ---- tool: repowise_hotspots ---------------------------------------------

type hotspotPayload struct {
	Path             string  `json:"path"`
	ChurnPercentile  float64 `json:"churnPercentile"`
	CommitCountTotal int     `json:"commitCountTotal"`
	PrimaryOwner     string  `json:"primaryOwner,omitempty"`
	BusFactor        int     `json:"busFactor"`
}

func (s *Server) toolHotspots(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := clampInt(req.GetInt("limit", 20), 1, 100)
	rows, err := s.opts.DB.QueryContext(ctx,
		`SELECT file_path, churn_percentile, commit_count_total,
		        COALESCE(primary_owner_name, ''), bus_factor
		 FROM git_metadata
		 WHERE repository_id = ? AND is_hotspot = 1
		 ORDER BY churn_percentile DESC, file_path
		 LIMIT ?`, s.opts.RepositoryID, limit)
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

// ---- tool: repowise_dead_code --------------------------------------------

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
	limit := clampInt(req.GetInt("limit", 50), 1, 500)
	safeOnly := req.GetBool("safe_only", false)

	query := `SELECT kind, file_path, COALESCE(symbol_name,''), COALESCE(symbol_kind,''),
	                 confidence, reason, safe_to_delete
	          FROM dead_code_findings WHERE repository_id = ?`
	args := []any{s.opts.RepositoryID}
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

// ---- tool: repowise_health -----------------------------------------------

type healthSummaryPayload struct {
	AverageScore       float64           `json:"averageScore"`
	FindingCount       int               `json:"findingCount"`
	FindingsByBiomarker map[string]int   `json:"findingsByBiomarker"`
	WorstFiles         []healthFileEntry `json:"worstFiles"`
}

type healthFileEntry struct {
	Path   string  `json:"path"`
	Score  float64 `json:"score"`
	MaxCCN int     `json:"maxCcn"`
}

func (s *Server) toolHealth(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	worstLimit := clampInt(req.GetInt("worst_limit", 10), 1, 50)
	hs := healthstore.New(s.opts.DB)
	p := healthSummaryPayload{}
	p.AverageScore, _ = hs.AverageScore(ctx, s.opts.RepositoryID)
	p.FindingCount, _ = hs.CountFindings(ctx, s.opts.RepositoryID)
	p.FindingsByBiomarker, _ = hs.CountByBiomarker(ctx, s.opts.RepositoryID)

	rows, err := s.opts.DB.QueryContext(ctx,
		`SELECT file_path, score, max_ccn FROM health_file_metrics
		 WHERE repository_id = ?
		 ORDER BY score ASC LIMIT ?`, s.opts.RepositoryID, worstLimit)
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

// ---- tool: repowise_health_findings --------------------------------------

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
	limit := clampInt(req.GetInt("limit", 50), 1, 500)
	biomarker := req.GetString("biomarker", "")

	query := `SELECT file_path, biomarker_type, severity, COALESCE(function_name,''),
	                 COALESCE(line_start,0), COALESCE(line_end,0), health_impact, reason
	          FROM health_findings WHERE repository_id = ?`
	args := []any{s.opts.RepositoryID}
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

// ---- tool: repowise_symbol -----------------------------------------------

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
		s.opts.RepositoryID, id)
	var p symbolPayload
	if err := row.Scan(&p.NodeID, &p.NodeType, &p.Kind, &p.Name, &p.QualifiedName,
		&p.FilePath, &p.StartLine, &p.EndLine, &p.Visibility, &p.Signature,
		&p.PageRank, &p.Language); err != nil {
		return mcp.NewToolResultError("symbol not found: " + id), nil
	}
	return jsonResult(p)
}

// ---- tool: repowise_callers ----------------------------------------------

type callerEdgePayload struct {
	From       string  `json:"from"`
	EdgeType   string  `json:"edgeType"`
	Confidence float64 `json:"confidence"`
}

func (s *Server) toolCallers(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		 LIMIT ?`, s.opts.RepositoryID, target, limit)
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

// ---- tool: repowise_dependents -------------------------------------------

type dependentEdgePayload struct {
	From          string   `json:"from"`
	Confidence    float64  `json:"confidence"`
	ImportedNames []string `json:"importedNames,omitempty"`
}

func (s *Server) toolDependents(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		s.opts.RepositoryID, target, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]dependentEdgePayload, 0, limit)
	for rows.Next() {
		var (
			d           dependentEdgePayload
			namesJSON   string
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

// ---- tool: repowise_externals --------------------------------------------

type externalPayload struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Ecosystem   string `json:"ecosystem"`
	Version     string `json:"version,omitempty"`
	DeclaredIn  string `json:"declaredIn"`
	IsDevDep    bool   `json:"isDevDep"`
}

func (s *Server) toolExternals(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := clampInt(req.GetInt("limit", 200), 1, 1000)
	ecosystem := req.GetString("ecosystem", "")
	devOnly := req.GetBool("dev_only", false)

	query := `SELECT name, display_name, ecosystem, COALESCE(version,''), declared_in, is_dev_dep
	          FROM external_systems WHERE repository_id = ?`
	args := []any{s.opts.RepositoryID}
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

// ---- tool: repowise_search -----------------------------------------------

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
	q, err := req.RequireString("query")
	if err != nil {
		return nil, err
	}
	limit := clampInt(req.GetInt("limit", 25), 1, 200)
	nodeType := req.GetString("node_type", "")

	pattern := "%" + q + "%"
	sqlQ := `SELECT node_id, node_type, COALESCE(kind,''), COALESCE(name,''),
	                COALESCE(file_path,''), COALESCE(start_line,0), pagerank
	         FROM graph_nodes
	         WHERE repository_id = ?
	           AND (name LIKE ? OR qualified_name LIKE ? OR node_id LIKE ?)`
	args := []any{s.opts.RepositoryID, pattern, pattern, pattern}
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

// ---- tool: repowise_pipeline_log ------------------------------------------

type pipelineLogEntry struct {
	Phase     string `json:"phase"`
	State     string `json:"state"`
	StartedAt string `json:"startedAt"`
	UpdatedAt string `json:"updatedAt"`
	Error     string `json:"error,omitempty"`
}

func (s *Server) toolPipelineLog(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := clampInt(req.GetInt("limit", 20), 1, 200)
	rows, err := s.opts.DB.QueryContext(ctx,
		`SELECT phase, state, started_at, updated_at, COALESCE(error,'')
		 FROM pipeline_jobs WHERE repository_id = ?
		 ORDER BY started_at DESC, id DESC LIMIT ?`,
		s.opts.RepositoryID, limit)
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
