package routes

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountAttention wires GET /api/repos/{repoID}/attention: a prioritized,
// cross-cutting "what should I look at" digest fusing knowledge silos,
// churn hotspots, dead code, and decisions awaiting review. One call so the
// Overview's Attention panel doesn't fan out per-source.
func MountAttention(r chi.Router, deps Deps) {
	r.Get("/attention", attention(deps))
}

type attentionItem struct {
	Category string `json:"category"` // knowledge_silo | ungoverned_hotspot | dead_code | needs_review
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Link     string `json:"link"` // repo-relative section, e.g. "hotspots"
}

func attention(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		repoID := httpx.URLParam(r, "repoID")
		items := make([]attentionItem, 0, 16)

		// Knowledge silos: active hotspots owned by a single contributor
		// (bus factor 1) — risky if that person leaves.
		silos, err := queryFiles(ctx, deps.DB,
			`SELECT file_path, COALESCE(primary_owner_name,''), commit_count_90d
			   FROM git_metadata
			  WHERE repository_id = ? AND bus_factor <= 1 AND commit_count_90d > 0
			  ORDER BY commit_count_90d DESC LIMIT 5`, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		for _, s := range silos {
			owner := s.text
			if owner == "" {
				owner = "unknown"
			}
			items = append(items, attentionItem{
				Category: "knowledge_silo",
				Title:    s.path,
				Detail:   fmt.Sprintf("single owner: %s · %d commits/90d", owner, s.n),
				Link:     "hotspots",
			})
		}

		// Churn hotspots with more than one contributor — high-traffic code
		// worth keeping an eye on (and ideally governing with a decision).
		hots, err := queryFiles(ctx, deps.DB,
			`SELECT file_path, '', commit_count_90d
			   FROM git_metadata
			  WHERE repository_id = ? AND is_hotspot = 1 AND bus_factor > 1
			  ORDER BY commit_count_90d DESC LIMIT 5`, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		for _, h := range hots {
			items = append(items, attentionItem{
				Category: "ungoverned_hotspot",
				Title:    h.path,
				Detail:   fmt.Sprintf("high churn · %d commits/90d", h.n),
				Link:     "hotspots",
			})
		}

		// Dead code: one summary item when there's anything safe to delete.
		var dcCount, dcLines int
		if err := deps.DB.QueryRowContext(ctx,
			`SELECT COUNT(*), COALESCE(SUM(lines),0)
			   FROM dead_code_findings
			  WHERE repository_id = ? AND safe_to_delete = 1`, repoID).Scan(&dcCount, &dcLines); err != nil {
			httpx.Internal(w, err)
			return
		}
		if dcCount > 0 {
			detail := "safe to remove"
			if dcLines > 0 {
				detail = fmt.Sprintf("%d lines can be removed", dcLines)
			}
			items = append(items, attentionItem{
				Category: "dead_code",
				Title:    fmt.Sprintf("%d safe-to-delete findings", dcCount),
				Detail:   detail,
				Link:     "dead-code",
			})
		}

		// Decisions awaiting review.
		decs, err := deps.DB.QueryContext(ctx,
			`SELECT title, verification
			   FROM decision_records
			  WHERE repository_id = ? AND verification IN ('unverified','proposed')
			  ORDER BY confidence DESC LIMIT 5`, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer decs.Close()
		for decs.Next() {
			var title, verification string
			if err := decs.Scan(&title, &verification); err != nil {
				httpx.Internal(w, err)
				return
			}
			items = append(items, attentionItem{
				Category: "needs_review",
				Title:    title,
				Detail:   verification,
				Link:     "decisions",
			})
		}
		if err := decs.Err(); err != nil {
			httpx.Internal(w, err)
			return
		}

		httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

type fileRow struct {
	path string
	text string
	n    int
}

// queryFiles runs a (path, text, count) query and collects the rows.
func queryFiles(ctx context.Context, db *sql.DB, query, repoID string) ([]fileRow, error) {
	rows, err := db.QueryContext(ctx, query, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []fileRow
	for rows.Next() {
		var fr fileRow
		if err := rows.Scan(&fr.path, &fr.text, &fr.n); err != nil {
			return nil, err
		}
		out = append(out, fr)
	}
	return out, rows.Err()
}
