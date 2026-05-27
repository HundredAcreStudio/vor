package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/repowise-dev/repowise-go/internal/server/http/httpx"
)

// MountHotspots registers /hotspots under /api/repos/{repoID}.
//
//	GET .../hotspots?limit=20    — files flagged as git hotspots, by
//	                                churn percentile descending.
func MountHotspots(r chi.Router, deps Deps) {
	r.Get("/hotspots", listHotspots(deps))
}

type hotspotDTO struct {
	Path             string   `json:"path"`
	ChurnPercentile  float64  `json:"churnPercentile"`
	CommitCountTotal int      `json:"commitCountTotal"`
	CommitCount90d   int      `json:"commitCount90d"`
	PrimaryOwner     string   `json:"primaryOwner,omitempty"`
	BusFactor        int      `json:"busFactor"`
	ContributorCount int      `json:"contributorCount"`
	LinesAdded90d    int      `json:"linesAdded90d"`
	LinesDeleted90d  int      `json:"linesDeleted90d"`
}

func listHotspots(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		limit := httpx.ParseIntQuery(r, "limit", 20)
		if limit > 200 {
			limit = 200
		}

		rows, err := deps.DB.QueryContext(r.Context(),
			`SELECT file_path, churn_percentile, commit_count_total, commit_count_90d,
			        COALESCE(primary_owner_name, ''), bus_factor, contributor_count,
			        lines_added_90d, lines_deleted_90d
			 FROM git_metadata
			 WHERE repository_id = ? AND is_hotspot = 1
			 ORDER BY churn_percentile DESC, file_path
			 LIMIT ?`, repoID, limit)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()

		out := make([]hotspotDTO, 0, limit)
		for rows.Next() {
			var h hotspotDTO
			if err := rows.Scan(&h.Path, &h.ChurnPercentile, &h.CommitCountTotal, &h.CommitCount90d,
				&h.PrimaryOwner, &h.BusFactor, &h.ContributorCount,
				&h.LinesAdded90d, &h.LinesDeleted90d); err != nil {
				httpx.Internal(w, err)
				return
			}
			out = append(out, h)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"hotspots": out,
			"limit":    limit,
		})
	}
}
