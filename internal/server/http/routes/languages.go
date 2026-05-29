package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountLanguages wires GET /api/repos/{repoID}/languages: the file-count
// distribution by language, for the Overview's languages donut.
func MountLanguages(r chi.Router, deps Deps) {
	r.Get("/languages", languages(deps))
}

type languageSlice struct {
	Language string `json:"language"`
	Files    int    `json:"files"`
}

func languages(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		rows, err := deps.DB.QueryContext(r.Context(),
			`SELECT COALESCE(language, ''), COUNT(*)
			   FROM graph_nodes
			  WHERE repository_id = ? AND node_type = 'file' AND COALESCE(language,'') != ''
			  GROUP BY language ORDER BY COUNT(*) DESC`, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()
		out := make([]languageSlice, 0, 12)
		total := 0
		for rows.Next() {
			var ls languageSlice
			if err := rows.Scan(&ls.Language, &ls.Files); err != nil {
				httpx.Internal(w, err)
				return
			}
			out = append(out, ls)
			total += ls.Files
		}
		if err := rows.Err(); err != nil {
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"languages": out, "total": total})
	}
}
