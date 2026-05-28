package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/repowise-dev/repowise-go/internal/server/http/httpx"
)

// MountGraph registers /graph endpoints under /api/repos/{repoID}.
// Two endpoints in Pass A:
//
//	GET .../graph/nodes        — paginated graph_nodes
//	GET .../graph/edges        — paginated graph_edges
func MountGraph(r chi.Router, deps Deps) {
	r.Get("/graph/nodes", listGraphNodes(deps))
	r.Get("/graph/edges", listGraphEdges(deps))
}

type graphNodeDTO struct {
	NodeID      string  `json:"nodeId"`
	NodeType    string  `json:"nodeType"`
	Language    string  `json:"language,omitempty"`
	SymbolCount int     `json:"symbolCount,omitempty"`
	PageRank    float64 `json:"pagerank,omitempty"`
	Betweenness float64 `json:"betweenness,omitempty"`
	CommunityID int     `json:"communityId,omitempty"`
	Kind        string  `json:"kind,omitempty"`
	Name        string  `json:"name,omitempty"`
	FilePath    string  `json:"filePath,omitempty"`
	StartLine   int     `json:"startLine,omitempty"`
	EndLine     int     `json:"endLine,omitempty"`
	Visibility  string  `json:"visibility,omitempty"`
}

func listGraphNodes(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		nodeType := r.URL.Query().Get("type") // optional: "file" or "symbol"
		limit := httpx.ParseIntQuery(r, "limit", 100)
		offset := httpx.ParseIntQuery(r, "offset", 0)
		if limit > 500 {
			limit = 500
		}

		var (
			rows *sqlRowsLike
			err  error
		)
		if nodeType != "" {
			rows, err = queryNodes(r, deps, repoID, nodeType, limit, offset)
		} else {
			rows, err = queryNodes(r, deps, repoID, "", limit, offset)
		}
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()

		nodes := make([]graphNodeDTO, 0, limit)
		for rows.Next() {
			var n graphNodeDTO
			var kind, name, filePath, vis sqlNullString
			var startLine, endLine sqlNullInt
			if err := rows.Scan(&n.NodeID, &n.NodeType, &n.Language,
				&n.SymbolCount, &n.PageRank, &n.Betweenness, &n.CommunityID,
				&kind, &name, &filePath, &startLine, &endLine, &vis,
			); err != nil {
				httpx.Internal(w, err)
				return
			}
			n.Kind = kind.String
			n.Name = name.String
			n.FilePath = filePath.String
			n.Visibility = vis.String
			if startLine.Valid {
				n.StartLine = int(startLine.Int64)
			}
			if endLine.Valid {
				n.EndLine = int(endLine.Int64)
			}
			nodes = append(nodes, n)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"nodes":  nodes,
			"limit":  limit,
			"offset": offset,
		})
	}
}

type graphEdgeDTO struct {
	Source        string   `json:"source"`
	Target        string   `json:"target"`
	EdgeType      string   `json:"edgeType"`
	Confidence    float64  `json:"confidence"`
	ImportedNames []string `json:"importedNames,omitempty"`
}

func listGraphEdges(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		edgeType := r.URL.Query().Get("type")
		limit := httpx.ParseIntQuery(r, "limit", 100)
		offset := httpx.ParseIntQuery(r, "offset", 0)
		if limit > 500 {
			limit = 500
		}

		rows, err := queryEdges(r, deps, repoID, edgeType, limit, offset)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()

		edges := make([]graphEdgeDTO, 0, limit)
		for rows.Next() {
			var e graphEdgeDTO
			var importedNames sqlNullString
			if err := rows.Scan(&e.Source, &e.Target, &e.EdgeType, &e.Confidence, &importedNames); err != nil {
				httpx.Internal(w, err)
				return
			}
			if importedNames.Valid && importedNames.String != "" && importedNames.String != "[]" {
				e.ImportedNames = parseJSONStringArray(importedNames.String)
			}
			edges = append(edges, e)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"edges":  edges,
			"limit":  limit,
			"offset": offset,
		})
	}
}
