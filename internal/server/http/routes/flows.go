package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/insights"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountFlows wires GET /api/repos/{repoID}/execution-flows: dependency/call
// trees rooted at the repo's entry points, backed by the shared insights layer.
func MountFlows(r chi.Router, deps Deps) {
	r.Get("/execution-flows", func(w http.ResponseWriter, r *http.Request) {
		flows, err := insights.ExecutionFlows(r.Context(), deps.DB, httpx.URLParam(r, "repoID"), insights.DefaultFlowOptions())
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"flows": flows})
	})
}
