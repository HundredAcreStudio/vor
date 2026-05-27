package routes

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

// queryNodes runs the graph_nodes query. nodeType="" means no type filter.
func queryNodes(r *http.Request, deps Deps, repoID, nodeType string, limit, offset int) (*sqlRowsLike, error) {
	query := `SELECT node_id, node_type, language, symbol_count, pagerank, betweenness, community_id,
	                 kind, name, file_path, start_line, end_line, visibility
	          FROM graph_nodes WHERE repository_id = ?`
	args := []any{repoID}
	if nodeType != "" {
		query += " AND node_type = ?"
		args = append(args, nodeType)
	}
	query += " ORDER BY pagerank DESC, node_id LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := deps.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		return nil, err
	}
	return &sqlRowsLike{rows}, nil
}

// queryEdges runs the graph_edges query.
func queryEdges(r *http.Request, deps Deps, repoID, edgeType string, limit, offset int) (*sqlRowsLike, error) {
	query := `SELECT source_node_id, target_node_id, edge_type, confidence, imported_names_json
	          FROM graph_edges WHERE repository_id = ?`
	args := []any{repoID}
	if edgeType != "" {
		query += " AND edge_type = ?"
		args = append(args, edgeType)
	}
	query += " ORDER BY source_node_id, target_node_id LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := deps.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		return nil, err
	}
	return &sqlRowsLike{rows}, nil
}

// sqlRowsLike is a thin wrapper around *sql.Rows so the handler files
// don't need to import database/sql directly.
type sqlRowsLike struct{ *sql.Rows }

// sqlNullString / sqlNullInt re-exports.
type sqlNullString = sql.NullString
type sqlNullInt = sql.NullInt64

// parseJSONStringArray decodes a `["a","b"]` JSON string into a Go slice.
// Returns nil on parse failure (handler treats it as no imported names).
func parseJSONStringArray(s string) []string {
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}
