package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/insights"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountAttention wires GET /api/repos/{repoID}/attention: the prioritized
// "what should I look at" digest (knowledge silos, churn hotspots, dead code,
// decisions awaiting review). Backed by the shared insights layer.
func MountAttention(r chi.Router, deps Deps) {
	r.Get("/attention", func(w http.ResponseWriter, r *http.Request) {
		items, err := insights.Attention(r.Context(), deps.DB, httpx.URLParam(r, "repoID"))
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
	})
}
