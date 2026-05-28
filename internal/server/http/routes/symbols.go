package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountSymbols registers /symbols, /callers, /dependents endpoints under
// /api/repos/{repoID}. URL path encoding lets callers escape "::" and
// "/" inside the symbol_id query parameter (we accept the raw form).
//
//	GET .../symbol?symbol_id=...      — detail for one symbol
//	GET .../callers?symbol_id=...     — incoming calls/has_method edges
//	GET .../dependents?file_path=... — incoming imports edges
func MountSymbols(r chi.Router, deps Deps) {
	r.Get("/symbol", getSymbol(deps))
	r.Get("/callers", getCallers(deps))
	r.Get("/dependents", getDependents(deps))
}

type symbolDetailDTO struct {
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

func getSymbol(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		symbolID := r.URL.Query().Get("symbol_id")
		if symbolID == "" {
			httpx.BadRequest(w, "symbol_id query parameter is required")
			return
		}

		row := deps.DB.QueryRowContext(r.Context(),
			`SELECT node_id, node_type, COALESCE(kind,''), COALESCE(name,''),
			        COALESCE(qualified_name,''), COALESCE(file_path,''),
			        COALESCE(start_line,0), COALESCE(end_line,0),
			        COALESCE(visibility,''), COALESCE(signature,''),
			        pagerank, language
			 FROM graph_nodes WHERE repository_id = ? AND node_id = ?`,
			repoID, symbolID)

		var d symbolDetailDTO
		if err := row.Scan(&d.NodeID, &d.NodeType, &d.Kind, &d.Name, &d.QualifiedName,
			&d.FilePath, &d.StartLine, &d.EndLine, &d.Visibility, &d.Signature,
			&d.PageRank, &d.Language); err != nil {
			if httpx.IsNoRows(err) {
				httpx.NotFound(w, "symbol")
				return
			}
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, d)
	}
}

type callerEdgeDTO struct {
	From       string  `json:"from"`
	EdgeType   string  `json:"edgeType"`
	Confidence float64 `json:"confidence"`
}

func getCallers(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		symbolID := r.URL.Query().Get("symbol_id")
		if symbolID == "" {
			httpx.BadRequest(w, "symbol_id query parameter is required")
			return
		}
		limit := httpx.ParseIntQuery(r, "limit", 50)
		if limit > 500 {
			limit = 500
		}

		rows, err := deps.DB.QueryContext(r.Context(),
			`SELECT source_node_id, edge_type, confidence
			 FROM graph_edges
			 WHERE repository_id = ? AND target_node_id = ?
			   AND edge_type IN ('calls', 'has_method', 'method_overrides', 'method_implements')
			 ORDER BY confidence DESC, source_node_id
			 LIMIT ?`, repoID, symbolID, limit)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()
		out := make([]callerEdgeDTO, 0, limit)
		for rows.Next() {
			var e callerEdgeDTO
			if err := rows.Scan(&e.From, &e.EdgeType, &e.Confidence); err != nil {
				httpx.Internal(w, err)
				return
			}
			out = append(out, e)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"symbolId": symbolID,
			"callers":  out,
		})
	}
}

type dependentEdgeDTO struct {
	From          string   `json:"from"`
	Confidence    float64  `json:"confidence"`
	ImportedNames []string `json:"importedNames,omitempty"`
}

func getDependents(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		filePath := r.URL.Query().Get("file_path")
		if filePath == "" {
			httpx.BadRequest(w, "file_path query parameter is required")
			return
		}
		limit := httpx.ParseIntQuery(r, "limit", 50)
		if limit > 500 {
			limit = 500
		}

		rows, err := deps.DB.QueryContext(r.Context(),
			`SELECT source_node_id, confidence, imported_names_json
			 FROM graph_edges
			 WHERE repository_id = ? AND target_node_id = ? AND edge_type = 'imports'
			 ORDER BY source_node_id LIMIT ?`, repoID, filePath, limit)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()
		out := make([]dependentEdgeDTO, 0, limit)
		for rows.Next() {
			var (
				d         dependentEdgeDTO
				namesJSON string
			)
			if err := rows.Scan(&d.From, &d.Confidence, &namesJSON); err != nil {
				httpx.Internal(w, err)
				return
			}
			if namesJSON != "" && namesJSON != "[]" {
				d.ImportedNames = parseJSONStringArray(namesJSON)
			}
			out = append(out, d)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"filePath":   filePath,
			"dependents": out,
		})
	}
}
