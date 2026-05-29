package insights

import (
	"context"
	"database/sql"
	"fmt"
)

// AttentionItem is one entry in the prioritized "what should I look at" digest.
type AttentionItem struct {
	Category string `json:"category"` // knowledge_silo | ungoverned_hotspot | dead_code | needs_review
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Link     string `json:"link"` // repo-relative dashboard section, e.g. "hotspots"
}

// Attention fuses cross-cutting signals into a prioritized digest: knowledge
// silos (single-owner active files), ungoverned churn hotspots, safe-to-delete
// dead code, and decisions awaiting review.
func Attention(ctx context.Context, db *sql.DB, repoID string) ([]AttentionItem, error) {
	items := make([]AttentionItem, 0, 16)

	silos, err := queryFiles(ctx, db,
		`SELECT file_path, COALESCE(primary_owner_name,''), commit_count_90d
		   FROM git_metadata
		  WHERE repository_id = ? AND bus_factor <= 1 AND commit_count_90d > 0
		  ORDER BY commit_count_90d DESC LIMIT 5`, repoID)
	if err != nil {
		return nil, err
	}
	for _, s := range silos {
		owner := s.text
		if owner == "" {
			owner = "unknown"
		}
		items = append(items, AttentionItem{
			Category: "knowledge_silo",
			Title:    s.path,
			Detail:   fmt.Sprintf("single owner: %s · %d commits/90d", owner, s.n),
			Link:     "hotspots",
		})
	}

	hots, err := queryFiles(ctx, db,
		`SELECT file_path, '', commit_count_90d
		   FROM git_metadata
		  WHERE repository_id = ? AND is_hotspot = 1 AND bus_factor > 1
		  ORDER BY commit_count_90d DESC LIMIT 5`, repoID)
	if err != nil {
		return nil, err
	}
	for _, h := range hots {
		items = append(items, AttentionItem{
			Category: "ungoverned_hotspot",
			Title:    h.path,
			Detail:   fmt.Sprintf("high churn · %d commits/90d", h.n),
			Link:     "hotspots",
		})
	}

	var dcCount, dcLines int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(lines),0)
		   FROM dead_code_findings
		  WHERE repository_id = ? AND safe_to_delete = 1`, repoID).Scan(&dcCount, &dcLines); err != nil {
		return nil, err
	}
	if dcCount > 0 {
		detail := "safe to remove"
		if dcLines > 0 {
			detail = fmt.Sprintf("%d lines can be removed", dcLines)
		}
		items = append(items, AttentionItem{
			Category: "dead_code",
			Title:    fmt.Sprintf("%d safe-to-delete findings", dcCount),
			Detail:   detail,
			Link:     "dead-code",
		})
	}

	rows, err := db.QueryContext(ctx,
		`SELECT title, verification
		   FROM decision_records
		  WHERE repository_id = ? AND verification IN ('unverified','proposed')
		  ORDER BY confidence DESC LIMIT 5`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var title, verification string
		if err := rows.Scan(&title, &verification); err != nil {
			return nil, err
		}
		items = append(items, AttentionItem{
			Category: "needs_review", Title: title, Detail: verification, Link: "decisions",
		})
	}
	return items, rows.Err()
}

type fileRow struct {
	path string
	text string
	n    int
}

// queryFiles runs a (path, text, count) query bound to one repoID.
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
