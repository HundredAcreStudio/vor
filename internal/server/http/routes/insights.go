package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/insights"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountInsights wires the overview's structural + git-insight endpoints under
// /api/repos/{repoID}, all backed by the shared insights layer:
//
//	GET .../communities       — graph clusters by size
//	GET .../entry-points      — indexed entry-point files
//	GET .../git-insights      — bus factor, churn distribution, top contributors
//	GET .../dependency-matrix — module×module import density
//	GET .../commit-categories — repo-level commit category tally
func MountInsights(r chi.Router, deps Deps) {
	r.Get("/communities", func(w http.ResponseWriter, r *http.Request) {
		v, err := insights.Communities(r.Context(), deps.DB, httpx.URLParam(r, "repoID"))
		respond(w, err, map[string]any{"communities": v})
	})
	r.Get("/entry-points", func(w http.ResponseWriter, r *http.Request) {
		v, err := insights.EntryPoints(r.Context(), deps.DB, httpx.URLParam(r, "repoID"))
		respond(w, err, map[string]any{"entryPoints": v})
	})
	r.Get("/git-insights", func(w http.ResponseWriter, r *http.Request) {
		v, err := insights.GitInsightsFor(r.Context(), deps.DB, httpx.URLParam(r, "repoID"))
		respond(w, err, v)
	})
	r.Get("/dependency-matrix", func(w http.ResponseWriter, r *http.Request) {
		v, err := insights.DependencyMatrixFor(r.Context(), deps.DB, httpx.URLParam(r, "repoID"))
		respond(w, err, v)
	})
	r.Get("/commit-categories", func(w http.ResponseWriter, r *http.Request) {
		v, err := insights.CommitCategories(r.Context(), deps.DB, httpx.URLParam(r, "repoID"))
		respond(w, err, v)
	})
}

// respond writes v as JSON, or a 500 when err is non-nil.
func respond(w http.ResponseWriter, err error, v any) {
	if err != nil {
		httpx.Internal(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}
