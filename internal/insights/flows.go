package insights

import (
	"context"
	"database/sql"
)

// FlowNode is a node in an execution-flow tree.
type FlowNode struct {
	Node     string     `json:"node"`
	Children []FlowNode `json:"children,omitempty"`
}

// FlowOptions bounds an execution-flow walk.
type FlowOptions struct {
	MaxDepth    int
	MaxRoots    int
	MaxChildren int
}

// DefaultFlowOptions are the dashboard's compact defaults.
func DefaultFlowOptions() FlowOptions {
	return FlowOptions{MaxDepth: 3, MaxRoots: 8, MaxChildren: 8}
}

// ExecutionFlows returns dependency/call trees rooted at the repo's indexed
// entry points, walking imports/calls to a bounded depth.
func ExecutionFlows(ctx context.Context, db *sql.DB, repoID string, opts FlowOptions) ([]FlowNode, error) {
	roots, err := flowRoots(ctx, db, repoID, opts.MaxRoots)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return []FlowNode{}, nil
	}
	adj, err := loadAdjacency(ctx, db, repoID)
	if err != nil {
		return nil, err
	}
	flows := make([]FlowNode, 0, len(roots))
	for _, root := range roots {
		flows = append(flows, buildFlow(adj, root, opts.MaxDepth, opts.MaxChildren, map[string]bool{}))
	}
	return flows, nil
}

func flowRoots(ctx context.Context, db *sql.DB, repoID string, limit int) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT node_id FROM graph_nodes
		 WHERE repository_id = ? AND is_entry_point = 1
		 ORDER BY pagerank DESC LIMIT ?`, repoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func loadAdjacency(ctx context.Context, db *sql.DB, repoID string) (map[string][]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT source_node_id, target_node_id
		  FROM graph_edges
		 WHERE repository_id = ? AND edge_type IN ('imports','calls','defines','has_method')
		 ORDER BY source_node_id, target_node_id`, repoID)
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

func buildFlow(adj map[string][]string, node string, depth, maxChildren int, visited map[string]bool) FlowNode {
	fn := FlowNode{Node: node}
	if depth <= 0 || visited[node] {
		return fn
	}
	visited[node] = true
	children := adj[node]
	if len(children) > maxChildren {
		children = children[:maxChildren]
	}
	for _, c := range children {
		if visited[c] {
			continue
		}
		fn.Children = append(fn.Children, buildFlow(adj, c, depth-1, maxChildren, visited))
	}
	return fn
}
