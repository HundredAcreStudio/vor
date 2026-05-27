package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/repowise-dev/repowise-go/internal/server/http/httpx"
)

// MountSearch registers /search under /api/repos/{repoID}.
//
//	GET .../search?q=...&type=symbol&limit=25
func MountSearch(r chi.Router, deps Deps) {
	r.Get("/search", searchSymbols(deps))
}

type searchHitDTO struct {
	NodeID    string  `json:"nodeId"`
	NodeType  string  `json:"nodeType"`
	Kind      string  `json:"kind,omitempty"`
	Name      string  `json:"name,omitempty"`
	FilePath  string  `json:"filePath,omitempty"`
	StartLine int     `json:"startLine,omitempty"`
	PageRank  float64 `json:"pagerank"`
}

func searchSymbols(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		q := r.URL.Query().Get("q")
		if q == "" {
			httpx.BadRequest(w, "q query parameter is required")
			return
		}
		limit := httpx.ParseIntQuery(r, "limit", 25)
		if limit > 200 {
			limit = 200
		}
		nodeType := r.URL.Query().Get("type")

		pattern := "%" + q + "%"
		sqlQ := `SELECT node_id, node_type, COALESCE(kind,''), COALESCE(name,''),
		                COALESCE(file_path,''), COALESCE(start_line,0), pagerank
		         FROM graph_nodes
		         WHERE repository_id = ?
		           AND (name LIKE ? OR qualified_name LIKE ? OR node_id LIKE ?)`
		args := []any{repoID, pattern, pattern, pattern}
		if nodeType != "" {
			sqlQ += " AND node_type = ?"
			args = append(args, nodeType)
		}
		sqlQ += " ORDER BY pagerank DESC, node_id LIMIT ?"
		args = append(args, limit)

		rows, err := deps.DB.QueryContext(r.Context(), sqlQ, args...)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()

		out := make([]searchHitDTO, 0, limit)
		for rows.Next() {
			var hit searchHitDTO
			if err := rows.Scan(&hit.NodeID, &hit.NodeType, &hit.Kind, &hit.Name,
				&hit.FilePath, &hit.StartLine, &hit.PageRank); err != nil {
				httpx.Internal(w, err)
				return
			}
			out = append(out, hit)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"query":   q,
			"matches": out,
			"limit":   limit,
		})
	}
}
