package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountExternals registers /externals under /api/repos/{repoID}.
//
//	GET .../externals?ecosystem=npm&dev=true   — manifest-declared deps
func MountExternals(r chi.Router, deps Deps) {
	r.Get("/externals", listExternals(deps))
}

type externalDTO struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Ecosystem   string `json:"ecosystem"`
	Category    string `json:"category"`
	Version     string `json:"version,omitempty"`
	DeclaredIn  string `json:"declaredIn"`
	IsDevDep    bool   `json:"isDevDep"`
}

func listExternals(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		ecosystem := r.URL.Query().Get("ecosystem")
		dev := r.URL.Query().Get("dev")
		limit := httpx.ParseIntQuery(r, "limit", 200)
		if limit > 1000 {
			limit = 1000
		}

		query := `SELECT name, display_name, ecosystem, category,
		                 COALESCE(version,''), declared_in, is_dev_dep
		          FROM external_systems WHERE repository_id = ?`
		args := []any{repoID}
		if ecosystem != "" {
			query += " AND ecosystem = ?"
			args = append(args, ecosystem)
		}
		switch dev {
		case "true":
			query += " AND is_dev_dep = 1"
		case "false":
			query += " AND is_dev_dep = 0"
		}
		query += " ORDER BY ecosystem, name LIMIT ?"
		args = append(args, limit)

		rows, err := deps.DB.QueryContext(r.Context(), query, args...)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()

		out := make([]externalDTO, 0, limit)
		for rows.Next() {
			var e externalDTO
			var isDev int
			if err := rows.Scan(&e.Name, &e.DisplayName, &e.Ecosystem, &e.Category,
				&e.Version, &e.DeclaredIn, &isDev); err != nil {
				httpx.Internal(w, err)
				return
			}
			e.IsDevDep = isDev == 1
			out = append(out, e)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"externals": out,
			"limit":     limit,
		})
	}
}
