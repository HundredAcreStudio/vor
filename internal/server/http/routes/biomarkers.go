package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/analysis/health"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountBiomarkers wires GET /api/biomarkers — the static catalog of code-health
// biomarkers (what each flags, how it's calculated, its thresholds). It's
// repo-independent: the same metric definitions power the Metrics reference
// page and the metric tooltips everywhere they're referenced.
func MountBiomarkers(r chi.Router, _ Deps) {
	r.Get("/biomarkers", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]any{"biomarkers": health.Biomarkers()})
	})
}
