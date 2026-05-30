package routes

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/insights"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountRisk wires GET /api/repos/{repoID}/risk — the risk roll-up the
// dashboard's Risk page renders: the five headline counts (hotspots, ownership
// silos, dead code, stale decisions, high security findings) plus the bus-factor
// distribution and top contributors (reused from the git-insights layer).
func MountRisk(r chi.Router, deps Deps) {
	r.Get("/risk", riskSummary(deps))
}

type riskCounts struct {
	Hotspots       int `json:"hotspots"`       // high-churn files
	Silos          int `json:"silos"`          // bus_factor<=1 with recent activity
	DeadCode       int `json:"deadCode"`       // dead-code findings
	StaleDecisions int `json:"staleDecisions"` // decisions needing review
	SecurityHigh   int `json:"securityHigh"`   // high/critical security findings
}

func riskSummary(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		repoID := httpx.URLParam(r, "repoID")

		counts := riskCounts{
			Hotspots:       scalarCount(ctx, deps.DB, `SELECT COUNT(*) FROM git_metadata WHERE repository_id = ? AND is_hotspot = 1`, repoID),
			Silos:          scalarCount(ctx, deps.DB, `SELECT COUNT(*) FROM git_metadata WHERE repository_id = ? AND bus_factor <= 1 AND commit_count_90d > 0`, repoID),
			DeadCode:       scalarCount(ctx, deps.DB, `SELECT COUNT(*) FROM dead_code_findings WHERE repository_id = ?`, repoID),
			StaleDecisions: scalarCount(ctx, deps.DB, `SELECT COUNT(*) FROM decision_records WHERE repository_id = ? AND verification IN ('unverified','proposed')`, repoID),
			SecurityHigh:   scalarCount(ctx, deps.DB, `SELECT COUNT(*) FROM security_findings WHERE repository_id = ? AND severity IN ('high','critical')`, repoID),
		}

		// Bus factor + top contributors come from the shared git-insights layer
		// (the same logic the Git Insights panel uses), so Risk stays consistent.
		gi, err := insights.GitInsightsFor(ctx, deps.DB, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}

		httpx.JSON(w, http.StatusOK, map[string]any{
			"counts":          counts,
			"busFactor":       gi.BusFactor,
			"churnBuckets":    gi.ChurnBuckets,
			"topContributors": gi.Contributors,
		})
	}
}

// scalarCount runs a COUNT(*) query and returns 0 on any error (best-effort:
// a missing/empty table shouldn't break the whole risk roll-up).
func scalarCount(ctx context.Context, db *sql.DB, query, repoID string) int {
	var n int
	_ = db.QueryRowContext(ctx, query, repoID).Scan(&n)
	return n
}
