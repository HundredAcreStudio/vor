package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// Graph-traversal tools. All four are deterministic queries over the
// persisted graph_nodes / graph_edges tables — no LLM. They mirror the
// Python implementation's get_community / get_dependency_path /
// get_execution_flows / get_architecture_diagram surface.

// ---- get_community -------------------------------------------------------

type communityMember struct {
	NodeID   string  `json:"node_id"`
	FilePath string  `json:"file_path,omitempty"`
	PageRank float64 `json:"pagerank"`
}

type communitySummary struct {
	CommunityID int               `json:"community_id"`
	Size        int               `json:"size"`
	Top         []communityMember `json:"top_members"`
}

func (s *Server) toolCommunity(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Targeted mode: return the members of the target's community.
	if target := req.GetString("target", ""); target != "" {
		var cid int
		err := s.opts.DB.QueryRowContext(ctx,
			`SELECT community_id FROM graph_nodes
			 WHERE repository_id = ? AND (node_id = ? OR file_path = ?) LIMIT 1`,
			rid, target, target).Scan(&cid)
		if err != nil {
			return mcp.NewToolResultError("target not found in graph: " + target), nil
		}
		members := s.communityMembers(ctx, rid, cid, 200)
		return jsonResult(map[string]any{
			"community_id": cid,
			"size":         len(members),
			"members":      members,
		})
	}

	// Survey mode: communities ranked by size, with their top members.
	limit := clampInt(req.GetInt("limit", 20), 1, 100)
	rows, err := s.opts.DB.QueryContext(ctx, `
		SELECT community_id, COUNT(*) AS n
		FROM graph_nodes
		WHERE repository_id = ? AND node_type = 'file'
		GROUP BY community_id
		ORDER BY n DESC
		LIMIT ?`, rid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []communitySummary{}
	for rows.Next() {
		var cs communitySummary
		if err := rows.Scan(&cs.CommunityID, &cs.Size); err != nil {
			return nil, err
		}
		cs.Top = s.communityMembers(ctx, rid, cs.CommunityID, 5)
		out = append(out, cs)
	}
	return jsonResult(map[string]any{"communities": out})
}

func (s *Server) communityMembers(ctx context.Context, rid string, cid, limit int) []communityMember {
	rows, err := s.opts.DB.QueryContext(ctx, `
		SELECT node_id, COALESCE(file_path, node_id), pagerank
		FROM graph_nodes
		WHERE repository_id = ? AND node_type = 'file' AND community_id = ?
		ORDER BY pagerank DESC
		LIMIT ?`, rid, cid, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []communityMember{}
	for rows.Next() {
		var m communityMember
		if err := rows.Scan(&m.NodeID, &m.FilePath, &m.PageRank); err == nil {
			out = append(out, m)
		}
	}
	return out
}

// ---- adjacency loading (shared by path + flows) --------------------------

// loadAdjacency builds an outbound adjacency map for the given repo,
// restricted to traversal-relevant edge types. Loaded once per tool
// call — bounded by the repo's edge count.
func (s *Server) loadAdjacency(ctx context.Context, rid string) (map[string][]string, error) {
	rows, err := s.opts.DB.QueryContext(ctx, `
		SELECT source_node_id, target_node_id
		FROM graph_edges
		WHERE repository_id = ?
		  AND edge_type IN ('imports', 'calls', 'defines', 'has_method')
		ORDER BY source_node_id, target_node_id`, rid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	adj := map[string][]string{}
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			return nil, err
		}
		adj[from] = append(adj[from], to)
	}
	return adj, rows.Err()
}

// ---- get_dependency_path -------------------------------------------------

func (s *Server) toolDependencyPath(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	from, err := req.RequireString("from")
	if err != nil {
		return mcp.NewToolResultError("`from` is required (a node_id or file path)"), nil
	}
	to, err := req.RequireString("to")
	if err != nil {
		return mcp.NewToolResultError("`to` is required (a node_id or file path)"), nil
	}
	from = s.canonicalNode(ctx, rid, from)
	to = s.canonicalNode(ctx, rid, to)

	adj, err := s.loadAdjacency(ctx, rid)
	if err != nil {
		return nil, err
	}
	path := bfsPath(adj, from, to)
	if path == nil {
		return jsonResult(map[string]any{
			"from": from, "to": to, "found": false,
			"note": "no dependency path in either direction within the indexed edges",
		})
	}
	return jsonResult(map[string]any{
		"from": from, "to": to, "found": true,
		"hops": len(path) - 1,
		"path": path,
	})
}

// canonicalNode resolves a file path to its node_id when the supplied
// string isn't already a node. Returns the input unchanged when it's
// already a node_id or no match exists.
func (s *Server) canonicalNode(ctx context.Context, rid, spec string) string {
	var nodeID string
	err := s.opts.DB.QueryRowContext(ctx,
		`SELECT node_id FROM graph_nodes
		 WHERE repository_id = ? AND (node_id = ? OR file_path = ?)
		 ORDER BY CASE WHEN node_id = ? THEN 0 ELSE 1 END LIMIT 1`,
		rid, spec, spec, spec).Scan(&nodeID)
	if err != nil {
		return spec
	}
	return nodeID
}

// bfsPath returns the shortest path from→to over adj, inclusive of both
// endpoints. nil when unreachable. from==to returns the single node.
func bfsPath(adj map[string][]string, from, to string) []string {
	if from == to {
		return []string{from}
	}
	prev := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if _, seen := prev[next]; seen {
				continue
			}
			prev[next] = cur
			if next == to {
				return reconstruct(prev, to)
			}
			queue = append(queue, next)
		}
	}
	return nil
}

func reconstruct(prev map[string]string, to string) []string {
	var rev []string
	for n := to; n != ""; n = prev[n] {
		rev = append(rev, n)
	}
	// reverse
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// ---- get_execution_flows -------------------------------------------------

type flowNode struct {
	NodeID   string     `json:"node_id"`
	Children []flowNode `json:"children,omitempty"`
}

func (s *Server) toolExecutionFlows(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	maxDepth := clampInt(req.GetInt("max_depth", 4), 1, 8)
	maxFlows := clampInt(req.GetInt("limit", 10), 1, 50)

	// Roots: an explicit entry, else the indexed entry points.
	var roots []string
	if entry := req.GetString("entry", ""); entry != "" {
		roots = []string{s.canonicalNode(ctx, rid, entry)}
	} else {
		roots = s.entryPoints(ctx, rid, maxFlows)
	}
	if len(roots) == 0 {
		return jsonResult(map[string]any{
			"flows": []flowNode{},
			"note":  "no entry points indexed (graph_nodes.is_entry_point) — pass an explicit `entry`",
		})
	}

	adj, err := s.loadAdjacency(ctx, rid)
	if err != nil {
		return nil, err
	}
	flows := make([]flowNode, 0, len(roots))
	for _, r := range roots {
		visited := map[string]bool{}
		flows = append(flows, buildFlow(adj, r, maxDepth, visited))
	}
	return jsonResult(map[string]any{"flows": flows, "max_depth": maxDepth})
}

func (s *Server) entryPoints(ctx context.Context, rid string, limit int) []string {
	rows, err := s.opts.DB.QueryContext(ctx, `
		SELECT node_id FROM graph_nodes
		WHERE repository_id = ? AND is_entry_point = 1
		ORDER BY pagerank DESC LIMIT ?`, rid, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// buildFlow walks adj outward from node to depth, guarding against
// cycles via the shared visited set. Children are capped to keep the
// tree readable.
func buildFlow(adj map[string][]string, node string, depth int, visited map[string]bool) flowNode {
	fn := flowNode{NodeID: node}
	if depth <= 0 || visited[node] {
		return fn
	}
	visited[node] = true
	const maxChildren = 12
	children := adj[node]
	if len(children) > maxChildren {
		children = children[:maxChildren]
	}
	for _, c := range children {
		if visited[c] {
			continue
		}
		fn.Children = append(fn.Children, buildFlow(adj, c, depth-1, visited))
	}
	return fn
}

// ---- get_architecture_diagram --------------------------------------------

// archComm + commPair are package-level so renderMermaid can take them
// by type (an anonymous-struct signature wouldn't unify with locals).
type archComm struct {
	id   int
	size int
	top  string
}

type commPair struct{ a, b int }

func (s *Server) toolArchitectureDiagram(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Communities by size (top 12 keep the diagram legible).
	communities := []archComm{}
	rows, err := s.opts.DB.QueryContext(ctx, `
		SELECT community_id, COUNT(*) n
		FROM graph_nodes WHERE repository_id = ? AND node_type = 'file'
		GROUP BY community_id ORDER BY n DESC LIMIT 12`, rid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c archComm
		if err := rows.Scan(&c.id, &c.size); err == nil {
			communities = append(communities, c)
		}
	}
	rows.Close()
	for i := range communities {
		// Representative label = highest-pagerank file in the community.
		_ = s.opts.DB.QueryRowContext(ctx, `
			SELECT COALESCE(file_path, node_id) FROM graph_nodes
			WHERE repository_id = ? AND node_type = 'file' AND community_id = ?
			ORDER BY pagerank DESC LIMIT 1`, rid, communities[i].id).Scan(&communities[i].top)
	}

	// Inter-community edge weights — how strongly clusters depend on
	// each other. file→community map first, then tally cross edges.
	fileComm := map[string]int{}
	frows, err := s.opts.DB.QueryContext(ctx, `
		SELECT node_id, community_id FROM graph_nodes
		WHERE repository_id = ? AND node_type = 'file'`, rid)
	if err != nil {
		return nil, err
	}
	for frows.Next() {
		var id string
		var cid int
		if err := frows.Scan(&id, &cid); err == nil {
			fileComm[id] = cid
		}
	}
	frows.Close()

	crossWeight := map[commPair]int{}
	erows, err := s.opts.DB.QueryContext(ctx, `
		SELECT source_node_id, target_node_id FROM graph_edges
		WHERE repository_id = ? AND edge_type = 'imports'`, rid)
	if err != nil {
		return nil, err
	}
	for erows.Next() {
		var from, to string
		if err := erows.Scan(&from, &to); err != nil {
			continue
		}
		ca, okA := fileComm[from]
		cb, okB := fileComm[to]
		if !okA || !okB || ca == cb {
			continue
		}
		if ca > cb {
			ca, cb = cb, ca
		}
		crossWeight[commPair{ca, cb}]++
	}
	erows.Close()

	// Entry points for the diagram's roots.
	entries := s.entryPoints(ctx, rid, 10)

	commDTO := make([]map[string]any, 0, len(communities))
	for _, c := range communities {
		commDTO = append(commDTO, map[string]any{
			"community_id": c.id, "size": c.size, "representative": c.top,
		})
	}
	edgeDTO := make([]map[string]any, 0, len(crossWeight))
	for p, w := range crossWeight {
		edgeDTO = append(edgeDTO, map[string]any{"from": p.a, "to": p.b, "weight": w})
	}
	sort.Slice(edgeDTO, func(i, j int) bool {
		return edgeDTO[i]["weight"].(int) > edgeDTO[j]["weight"].(int)
	})

	result := map[string]any{
		"communities":  commDTO,
		"dependencies": edgeDTO,
		"entry_points": entries,
	}
	if req.GetString("format", "") == "mermaid" {
		result["mermaid"] = renderMermaid(communities, crossWeight)
	}
	return jsonResult(result)
}

// renderMermaid emits a flowchart of communities + their cross-cluster
// import edges. Clients that render mermaid get a picture; everyone
// else has the structured fields.
func renderMermaid(communities []archComm, cross map[commPair]int) string {
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	for _, c := range communities {
		label := c.top
		if label == "" {
			label = "community"
		}
		fmt.Fprintf(&b, "  C%d[\"%s (%d files)\"]\n", c.id, sanitizeMermaidLabel(label), c.size)
	}
	for p, w := range cross {
		fmt.Fprintf(&b, "  C%d -->|%d| C%d\n", p.a, w, p.b)
	}
	return b.String()
}

// sanitizeMermaidLabel strips characters that would break mermaid node
// labels (quotes, brackets, pipes).
func sanitizeMermaidLabel(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '"', '[', ']', '|', '{', '}':
			return '_'
		}
		return r
	}, s)
}

// silence sql import if a future refactor drops the direct use.
var _ = sql.ErrNoRows
