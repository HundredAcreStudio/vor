package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/insights"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountArchitecture wires the module/package structure endpoints under
// /api/repos/{repoID}, backed by the shared insights layer:
//
//	GET .../modules   — per-module file/symbol counts, docs coverage, deps
//	GET .../packages  — detected packages + monorepo flag
func MountArchitecture(r chi.Router, deps Deps) {
	r.Get("/modules", func(w http.ResponseWriter, r *http.Request) {
		v, err := insights.Modules(r.Context(), deps.DB, httpx.URLParam(r, "repoID"))
		respond(w, err, map[string]any{"modules": v})
	})
	r.Get("/packages", func(w http.ResponseWriter, r *http.Request) {
		v, err := insights.Packages(r.Context(), deps.DB, httpx.URLParam(r, "repoID"))
		respond(w, err, v)
	})
}
