package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/insights"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountLanguages wires GET /api/repos/{repoID}/languages: the file-count
// distribution by language, backed by the shared insights layer.
func MountLanguages(r chi.Router, deps Deps) {
	r.Get("/languages", func(w http.ResponseWriter, r *http.Request) {
		v, err := insights.Languages(r.Context(), deps.DB, httpx.URLParam(r, "repoID"))
		respond(w, err, v)
	})
}
