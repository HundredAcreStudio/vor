package routes

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/analysis/health"
	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/persistence/settingsstore"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountSettings wires the per-repo settings endpoints under
// /api/repos/{repoID}.
//
//	GET    .../settings        — effective config + which keys this repo overrides
//	PUT    .../settings/{key}  — set a repo-scoped override (raw JSON body)
//	DELETE .../settings/{key}  — clear a repo-scoped override
func MountSettings(r chi.Router, deps Deps) {
	r.Get("/settings", getSettings(deps))
	r.Put("/settings/{key}", putSetting(deps))
	r.Delete("/settings/{key}", deleteSetting(deps))
}

func getSettings(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		cfg, err := config.Resolve(r.Context(), deps.DB, repoID, config.LoadBootstrap())
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		// Which keys are set at this repo's scope (vs. inherited from global
		// defaults) — lets the UI show what's been customised here.
		overrides, err := settingsstore.New(deps.DB).GetScope(r.Context(), repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		overridden := make(map[string]bool, len(overrides))
		for k := range overrides {
			overridden[k] = true
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"effective":  cfg, // marshals with the setting-key json tags
			"overridden": overridden,
			"biomarkers": health.AllBiomarkers(),
		})
	}
}

func putSetting(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		key := httpx.URLParam(r, "key")
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			httpx.BadRequest(w, "could not read body")
			return
		}
		if err := config.ValidateSetting(key, string(raw)); err != nil {
			httpx.BadRequest(w, err.Error())
			return
		}
		if err := settingsstore.New(deps.DB).Set(r.Context(), repoID, key, string(raw)); err != nil {
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"key": key})
	}
}

func deleteSetting(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		key := httpx.URLParam(r, "key")
		if err := settingsstore.New(deps.DB).Delete(r.Context(), repoID, key); err != nil {
			httpx.Internal(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
