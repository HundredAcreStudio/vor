package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/persistence/healthstore"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountHealth registers /health endpoints under /api/repos/{repoID}.
//
//	GET .../health           — aggregate KPI: avg score + biomarker tally
//	GET .../health/findings  — paginated list of health_findings
//	GET .../health/files     — paginated per-file metrics
func MountHealth(r chi.Router, deps Deps) {
	store := healthstore.New(deps.DB)
	r.Get("/health", healthSummary(deps, store))
	r.Get("/health/findings", healthFindings(deps))
	r.Get("/health/files", healthFiles(deps))
}

type healthSummaryDTO struct {
	AverageScore        float64        `json:"averageScore"`
	FindingCount        int            `json:"findingCount"`
	FindingsByBiomarker map[string]int `json:"findingsByBiomarker"`
}

func healthSummary(deps Deps, store *healthstore.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		avg, err := store.AverageScore(r.Context(), repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		count, err := store.CountFindings(r.Context(), repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		byKind, err := store.CountByBiomarker(r.Context(), repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, healthSummaryDTO{
			AverageScore:        avg,
			FindingCount:        count,
			FindingsByBiomarker: byKind,
		})
	}
}

type healthFindingDTO struct {
	FilePath      string  `json:"filePath"`
	BiomarkerType string  `json:"biomarkerType"`
	Severity      string  `json:"severity"`
	FunctionName  string  `json:"functionName,omitempty"`
	LineStart     int     `json:"lineStart,omitempty"`
	LineEnd       int     `json:"lineEnd,omitempty"`
	HealthImpact  float64 `json:"healthImpact"`
	Reason        string  `json:"reason"`
}

func healthFindings(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		limit := httpx.ParseIntQuery(r, "limit", 100)
		if limit > 500 {
			limit = 500
		}
		biomarker := r.URL.Query().Get("biomarker")

		query := `SELECT file_path, biomarker_type, severity, COALESCE(function_name,''),
		                 COALESCE(line_start,0), COALESCE(line_end,0), health_impact, reason
		          FROM health_findings WHERE repository_id = ?`
		args := []any{repoID}
		if biomarker != "" {
			query += " AND biomarker_type = ?"
			args = append(args, biomarker)
		}
		query += ` ORDER BY CASE severity
		                     WHEN 'critical' THEN 0
		                     WHEN 'high'     THEN 1
		                     WHEN 'medium'   THEN 2
		                     ELSE 3 END, health_impact DESC LIMIT ?`
		args = append(args, limit)

		rows, err := deps.DB.QueryContext(r.Context(), query, args...)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()

		out := make([]healthFindingDTO, 0, limit)
		for rows.Next() {
			var f healthFindingDTO
			if err := rows.Scan(&f.FilePath, &f.BiomarkerType, &f.Severity, &f.FunctionName,
				&f.LineStart, &f.LineEnd, &f.HealthImpact, &f.Reason); err != nil {
				httpx.Internal(w, err)
				return
			}
			out = append(out, f)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"findings": out,
			"limit":    limit,
		})
	}
}

type healthFileDTO struct {
	FilePath   string  `json:"filePath"`
	Score      float64 `json:"score"`
	MaxCCN     int     `json:"maxCcn"`
	MaxNesting int     `json:"maxNesting"`
	NLOC       int     `json:"nloc"`
}

func healthFiles(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		limit := httpx.ParseIntQuery(r, "limit", 100)
		if limit > 500 {
			limit = 500
		}
		order := r.URL.Query().Get("order") // "score" (default asc), "score_desc", "max_ccn"
		orderClause := "score ASC"
		switch order {
		case "score_desc":
			orderClause = "score DESC"
		case "max_ccn":
			orderClause = "max_ccn DESC"
		}

		rows, err := deps.DB.QueryContext(r.Context(),
			`SELECT file_path, score, max_ccn, max_nesting, nloc
			 FROM health_file_metrics WHERE repository_id = ?
			 ORDER BY `+orderClause+` LIMIT ?`, repoID, limit)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()

		out := make([]healthFileDTO, 0, limit)
		for rows.Next() {
			var f healthFileDTO
			if err := rows.Scan(&f.FilePath, &f.Score, &f.MaxCCN, &f.MaxNesting, &f.NLOC); err != nil {
				httpx.Internal(w, err)
				return
			}
			out = append(out, f)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"files": out,
			"limit": limit,
		})
	}
}
