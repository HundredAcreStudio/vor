package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/repowise-dev/repowise-go/internal/server/http/httpx"
)

// MountDeadCode registers /dead-code under /api/repos/{repoID}.
//
//	GET .../dead-code?min_confidence=0.5&limit=50&safe_only=true
func MountDeadCode(r chi.Router, deps Deps) {
	r.Get("/dead-code", listDeadCode(deps))
}

type deadCodeDTO struct {
	Kind         string  `json:"kind"`
	FilePath     string  `json:"filePath"`
	SymbolName   string  `json:"symbolName,omitempty"`
	SymbolKind   string  `json:"symbolKind,omitempty"`
	Confidence   float64 `json:"confidence"`
	Reason       string  `json:"reason"`
	SafeToDelete bool    `json:"safeToDelete"`
}

func listDeadCode(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		limit := httpx.ParseIntQuery(r, "limit", 50)
		if limit > 500 {
			limit = 500
		}
		safeOnly := r.URL.Query().Get("safe_only") == "true"

		query := `SELECT kind, file_path, COALESCE(symbol_name,''), COALESCE(symbol_kind,''),
		                 confidence, reason, safe_to_delete
		          FROM dead_code_findings
		          WHERE repository_id = ?`
		args := []any{repoID}
		if safeOnly {
			query += " AND safe_to_delete = 1"
		}
		// Allow a confidence floor; sneak it past the query helper rather
		// than building extra plumbing for one feature.
		if mc := r.URL.Query().Get("min_confidence"); mc != "" {
			query += " AND confidence >= ?"
			args = append(args, mc)
		}
		query += " ORDER BY confidence DESC, file_path LIMIT ?"
		args = append(args, limit)

		rows, err := deps.DB.QueryContext(r.Context(), query, args...)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()

		out := make([]deadCodeDTO, 0, limit)
		for rows.Next() {
			var d deadCodeDTO
			var safe int
			if err := rows.Scan(&d.Kind, &d.FilePath, &d.SymbolName, &d.SymbolKind,
				&d.Confidence, &d.Reason, &safe); err != nil {
				httpx.Internal(w, err)
				return
			}
			d.SafeToDelete = safe == 1
			out = append(out, d)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"findings": out,
			"limit":    limit,
		})
	}
}
