package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountDecisions registers /decisions under /api/repos/{repoID}.
//
//	GET .../decisions?source=inline_marker&limit=50
func MountDecisions(r chi.Router, deps Deps) {
	r.Get("/decisions", listDecisions(deps))
}

type decisionDTO struct {
	Title        string  `json:"title"`
	Source       string  `json:"source"`
	EvidenceFile string  `json:"evidenceFile,omitempty"`
	EvidenceLine int     `json:"evidenceLine,omitempty"`
	Confidence   float64 `json:"confidence"`
	Verification string  `json:"verification"`
	Decision     string  `json:"decision,omitempty"`
	Rationale    string  `json:"rationale,omitempty"`
	SourceQuote  string  `json:"sourceQuote,omitempty"`
}

func listDecisions(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		source := r.URL.Query().Get("source")
		limit := httpx.ParseIntQuery(r, "limit", 50)
		if limit > 500 {
			limit = 500
		}

		query := `SELECT dr.title, dr.source, COALESCE(dr.evidence_file,''),
		                 COALESCE(dr.evidence_line,0), dr.confidence, dr.verification,
		                 dr.decision, dr.rationale,
		                 COALESCE((SELECT source_quote FROM decision_evidence de
		                           WHERE de.decision_id = dr.id LIMIT 1), '')
		          FROM decision_records dr
		          WHERE dr.repository_id = ?`
		args := []any{repoID}
		if source != "" {
			query += " AND dr.source = ?"
			args = append(args, source)
		}
		query += " ORDER BY dr.confidence DESC, dr.evidence_file, dr.evidence_line LIMIT ?"
		args = append(args, limit)

		rows, err := deps.DB.QueryContext(r.Context(), query, args...)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()
		out := make([]decisionDTO, 0, limit)
		for rows.Next() {
			var d decisionDTO
			if err := rows.Scan(&d.Title, &d.Source, &d.EvidenceFile, &d.EvidenceLine,
				&d.Confidence, &d.Verification, &d.Decision, &d.Rationale, &d.SourceQuote); err != nil {
				httpx.Internal(w, err)
				return
			}
			out = append(out, d)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"decisions": out,
			"limit":     limit,
		})
	}
}
