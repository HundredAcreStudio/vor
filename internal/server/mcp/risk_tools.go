package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// riskTarget is the per-file modification-risk assessment vor_risk returns.
type riskTarget struct {
	Target          string   `json:"target"`
	FilePath        string   `json:"file_path"`
	Indexed         bool     `json:"indexed"`
	Hotspot         bool     `json:"hotspot"`
	ChurnPercentile float64  `json:"churn_percentile"`
	CommitCount90d  int      `json:"commit_count_90d"`
	Dependents      int      `json:"dependents"`
	TopDependents   []string `json:"top_dependents,omitempty"`
	CoChange        []string `json:"co_change_partners,omitempty"`
	PrimaryOwner    string   `json:"primary_owner,omitempty"`
	BusFactor       int      `json:"bus_factor"`
	Contributors    int      `json:"contributor_count"`
	Decisions       []string `json:"governing_decisions,omitempty"`
	Risk            string   `json:"risk"` // high | medium | low | unknown
	Reasons         []string `json:"reasons,omitempty"`
}

// toolRisk assesses the blast radius and risk of modifying one or more files,
// fusing git churn/ownership, graph dependents, co-change partners, and the
// architectural decisions that govern them.
func (s *Server) toolRisk(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	targets := req.GetStringSlice("targets", nil)
	if len(targets) == 0 {
		if one := req.GetString("target", ""); one != "" {
			targets = []string{one}
		}
	}
	if len(targets) == 0 {
		return mcp.NewToolResultError("provide `targets` (array) or `target` (string)"), nil
	}
	out := make([]riskTarget, 0, len(targets))
	for _, t := range targets {
		out = append(out, s.assessRisk(ctx, rid, t))
	}
	return jsonResult(map[string]any{"targets": out})
}

func (s *Server) assessRisk(ctx context.Context, rid, target string) riskTarget {
	node := s.canonicalNode(ctx, rid, target)
	fp := node
	if i := strings.Index(fp, "::"); i >= 0 {
		fp = fp[:i]
	}
	rt := riskTarget{Target: target, FilePath: fp}

	var (
		isHot          sql.NullBool
		churn          sql.NullFloat64
		c90            sql.NullInt64
		owner          sql.NullString
		bus, contrib   sql.NullInt64
		coChangePartner sql.NullString
	)
	err := s.opts.DB.QueryRowContext(ctx, `
		SELECT is_hotspot, churn_percentile, commit_count_90d, primary_owner_name,
		       bus_factor, contributor_count, co_change_partners_json
		  FROM git_metadata WHERE repository_id = ? AND file_path = ?`, rid, fp).
		Scan(&isHot, &churn, &c90, &owner, &bus, &contrib, &coChangePartner)
	if err == nil {
		rt.Indexed = true
		rt.Hotspot = isHot.Valid && isHot.Bool
		rt.ChurnPercentile = churn.Float64
		rt.CommitCount90d = int(c90.Int64)
		rt.PrimaryOwner = owner.String
		rt.BusFactor = int(bus.Int64)
		rt.Contributors = int(contrib.Int64)
		if coChangePartner.Valid && coChangePartner.String != "" {
			var partners []struct {
				Path  string
				Count int
			}
			if json.Unmarshal([]byte(coChangePartner.String), &partners) == nil {
				for i, p := range partners {
					if i >= 5 {
						break
					}
					rt.CoChange = append(rt.CoChange, p.Path)
				}
			}
		}
	}

	// Dependents: files that import this one (incoming import edges).
	if rows, derr := s.opts.DB.QueryContext(ctx, `
		SELECT source_node_id FROM graph_edges
		 WHERE repository_id = ? AND target_node_id = ? AND edge_type = 'imports'
		 ORDER BY source_node_id LIMIT 500`, rid, node); derr == nil {
		for rows.Next() {
			var src string
			if rows.Scan(&src) == nil {
				rt.Dependents++
				if len(rt.TopDependents) < 8 {
					rt.TopDependents = append(rt.TopDependents, src)
				}
			}
		}
		rows.Close()
	}

	// Governing decisions: records that name this file.
	if dr, err2 := s.opts.DB.QueryContext(ctx, `
		SELECT title FROM decision_records
		 WHERE repository_id = ? AND (evidence_file = ? OR affected_files_json LIKE ?)
		 ORDER BY confidence DESC LIMIT 10`, rid, fp, "%\""+fp+"\"%"); err2 == nil {
		for dr.Next() {
			var title string
			if dr.Scan(&title) == nil {
				rt.Decisions = append(rt.Decisions, title)
			}
		}
		dr.Close()
	}

	rt.Risk, rt.Reasons = deriveRisk(rt)
	return rt
}

// attentionItem is one entry in the vor_attention digest.
type attentionItem struct {
	Category string `json:"category"` // knowledge_silo | ungoverned_hotspot | dead_code | needs_review
	Title    string `json:"title"`
	Detail   string `json:"detail"`
}

// toolAttention returns a prioritized "what should I look at" digest — the
// same cross-cutting signal the dashboard's Attention panel shows: knowledge
// silos, churn hotspots, dead code, and decisions awaiting review.
func (s *Server) toolAttention(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	items := make([]attentionItem, 0, 16)

	type fileRow struct {
		path  string
		owner string
		n     int
	}
	scan := func(query string) []fileRow {
		rows, qerr := s.opts.DB.QueryContext(ctx, query, rid)
		if qerr != nil {
			return nil
		}
		defer rows.Close()
		var out []fileRow
		for rows.Next() {
			var f fileRow
			if rows.Scan(&f.path, &f.owner, &f.n) == nil {
				out = append(out, f)
			}
		}
		return out
	}

	for _, f := range scan(`SELECT file_path, COALESCE(primary_owner_name,''), commit_count_90d
		FROM git_metadata WHERE repository_id = ? AND bus_factor <= 1 AND commit_count_90d > 0
		ORDER BY commit_count_90d DESC LIMIT 5`) {
		owner := f.owner
		if owner == "" {
			owner = "unknown"
		}
		items = append(items, attentionItem{"knowledge_silo", f.path,
			fmt.Sprintf("single owner: %s · %d commits/90d", owner, f.n)})
	}
	for _, f := range scan(`SELECT file_path, '', commit_count_90d
		FROM git_metadata WHERE repository_id = ? AND is_hotspot = 1 AND bus_factor > 1
		ORDER BY commit_count_90d DESC LIMIT 5`) {
		items = append(items, attentionItem{"ungoverned_hotspot", f.path,
			fmt.Sprintf("high churn · %d commits/90d", f.n)})
	}
	var dcCount, dcLines int
	_ = s.opts.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(lines),0)
		FROM dead_code_findings WHERE repository_id = ? AND safe_to_delete = 1`, rid).Scan(&dcCount, &dcLines)
	if dcCount > 0 {
		items = append(items, attentionItem{"dead_code",
			fmt.Sprintf("%d safe-to-delete findings", dcCount),
			fmt.Sprintf("%d lines can be removed", dcLines)})
	}
	if rows, derr := s.opts.DB.QueryContext(ctx, `SELECT title, verification
		FROM decision_records WHERE repository_id = ? AND verification IN ('unverified','proposed')
		ORDER BY confidence DESC LIMIT 5`, rid); derr == nil {
		for rows.Next() {
			var title, verification string
			if rows.Scan(&title, &verification) == nil {
				items = append(items, attentionItem{"needs_review", title, verification})
			}
		}
		rows.Close()
	}

	return jsonResult(map[string]any{"items": items})
}

// deriveRisk turns the gathered signals into a level + human reasons.
func deriveRisk(rt riskTarget) (string, []string) {
	if !rt.Indexed {
		return "unknown", []string{"file not indexed — no risk signals available"}
	}
	var reasons []string
	score := 0
	if rt.Hotspot || rt.ChurnPercentile >= 0.9 {
		score += 2
		reasons = append(reasons, "high-churn hotspot — changes here are frequent and error-prone")
	}
	switch {
	case rt.Dependents >= 10:
		score += 2
		reasons = append(reasons, fmt.Sprintf("%d dependents — wide blast radius, API changes will ripple", rt.Dependents))
	case rt.Dependents >= 3:
		score++
		reasons = append(reasons, fmt.Sprintf("%d dependents", rt.Dependents))
	}
	if rt.BusFactor <= 1 {
		score++
		reasons = append(reasons, "bus factor 1 — single owner, route review to them")
	}
	if len(rt.CoChange) > 0 {
		reasons = append(reasons, fmt.Sprintf("often changes with %d other file(s) — you may need to update them too", len(rt.CoChange)))
	}
	if len(rt.Decisions) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d governing decision(s) — check before diverging", len(rt.Decisions)))
	}
	switch {
	case score >= 3:
		return "high", reasons
	case score >= 1:
		return "medium", reasons
	default:
		return "low", reasons
	}
}
