package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/persistence/pipelinestore"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountPipeline registers /pipeline/* under /api/repos/{repoID}.
//
//	GET .../pipeline/log     — recent pipeline_jobs rows DESC by started_at
//	GET .../pipeline/summary — per-state row counts
func MountPipeline(r chi.Router, deps Deps) {
	store := pipelinestore.New(deps.DB)
	r.Get("/pipeline/log", pipelineLog(deps, store))
	r.Get("/pipeline/summary", pipelineSummary(store))
}

type pipelineLogEntryDTO struct {
	Phase     string `json:"phase"`
	State     string `json:"state"`
	StartedAt string `json:"startedAt"`
	UpdatedAt string `json:"updatedAt"`
	Error     string `json:"error,omitempty"`
}

func pipelineLog(deps Deps, store *pipelinestore.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		limit := httpx.ParseIntQuery(r, "limit", 50)
		if limit > 500 {
			limit = 500
		}
		rows, err := store.LatestByRepo(r.Context(), repoID, limit)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		out := make([]pipelineLogEntryDTO, 0, len(rows))
		for _, j := range rows {
			out = append(out, pipelineLogEntryDTO{
				Phase:     j.Phase,
				State:     j.State,
				StartedAt: j.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
				UpdatedAt: j.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
				Error:     j.Error,
			})
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"entries": out,
			"limit":   limit,
		})
	}
}

func pipelineSummary(store *pipelinestore.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		counts, err := store.CountByState(r.Context(), repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		// Stable shape: emit every state key even when zero.
		out := map[string]int{
			pipelinestore.StatePending:   counts[pipelinestore.StatePending],
			pipelinestore.StateRunning:   counts[pipelinestore.StateRunning],
			pipelinestore.StateCompleted: counts[pipelinestore.StateCompleted],
			pipelinestore.StateFailed:    counts[pipelinestore.StateFailed],
		}
		httpx.JSON(w, http.StatusOK, out)
	}
}
