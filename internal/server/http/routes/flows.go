package routes

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountFlows wires GET /api/repos/{repoID}/execution-flows: dependency/call
// trees rooted at the repo's indexed entry points. Ported from the MCP
// get_execution_flows tool so the dashboard can render the same trees.
func MountFlows(r chi.Router, deps Deps) {
	r.Get("/execution-flows", executionFlows(deps))
}

type flowNode struct {
	Node     string     `json:"node"`
	Children []flowNode `json:"children,omitempty"`
}

const (
	flowMaxDepth    = 3
	flowMaxRoots    = 8
	flowMaxChildren = 8
)

func executionFlows(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		repoID := httpx.URLParam(r, "repoID")

		roots, err := flowRoots(ctx, deps.DB, repoID, flowMaxRoots)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		if len(roots) == 0 {
			httpx.JSON(w, http.StatusOK, map[string]any{
				"flows": []flowNode{},
				"note":  "no entry points indexed for this repo",
			})
			return
		}
		adj, err := loadAdjacency(ctx, deps.DB, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		flows := make([]flowNode, 0, len(roots))
		for _, root := range roots {
			flows = append(flows, buildFlow(adj, root, flowMaxDepth, map[string]bool{}))
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"flows": flows})
	}
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

// buildFlow walks adj outward to depth, guarding cycles via the shared
// visited set and capping fan-out for readability.
func buildFlow(adj map[string][]string, node string, depth int, visited map[string]bool) flowNode {
	fn := flowNode{Node: node}
	if depth <= 0 || visited[node] {
		return fn
	}
	visited[node] = true
	children := adj[node]
	if len(children) > flowMaxChildren {
		children = children[:flowMaxChildren]
	}
	for _, c := range children {
		if visited[c] {
			continue
		}
		fn.Children = append(fn.Children, buildFlow(adj, c, depth-1, visited))
	}
	return fn
}
