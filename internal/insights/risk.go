package insights

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// RiskTarget is the per-file modification-risk assessment.
type RiskTarget struct {
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

// Risk assesses the blast radius and risk of modifying each target file,
// fusing git churn/ownership, graph dependents, co-change partners, and the
// architectural decisions that govern them.
func Risk(ctx context.Context, db *sql.DB, repoID string, targets []string) ([]RiskTarget, error) {
	out := make([]RiskTarget, 0, len(targets))
	for _, t := range targets {
		rt, err := assessRisk(ctx, db, repoID, t)
		if err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, nil
}

func assessRisk(ctx context.Context, db *sql.DB, repoID, target string) (RiskTarget, error) {
	fp := filePathOf(target)
	rt := RiskTarget{Target: target, FilePath: fp}

	var (
		isHot           sql.NullBool
		churn           sql.NullFloat64
		c90             sql.NullInt64
		owner           sql.NullString
		bus, contrib    sql.NullInt64
		coChangePartner sql.NullString
	)
	err := db.QueryRowContext(ctx, `
		SELECT is_hotspot, churn_percentile, commit_count_90d, primary_owner_name,
		       bus_factor, contributor_count, co_change_partners_json
		  FROM git_metadata WHERE repository_id = ? AND file_path = ?`, repoID, fp).
		Scan(&isHot, &churn, &c90, &owner, &bus, &contrib, &coChangePartner)
	if err != nil && err != sql.ErrNoRows {
		return rt, err
	}
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

	// Dependents: files that import this one (incoming import edges). For file
	// nodes the node_id is the file path.
	dep, err := db.QueryContext(ctx, `
		SELECT source_node_id FROM graph_edges
		 WHERE repository_id = ? AND target_node_id = ? AND edge_type = 'imports'
		 ORDER BY source_node_id LIMIT 500`, repoID, fp)
	if err != nil {
		return rt, err
	}
	for dep.Next() {
		var src string
		if err := dep.Scan(&src); err != nil {
			dep.Close()
			return rt, err
		}
		rt.Dependents++
		if len(rt.TopDependents) < 8 {
			rt.TopDependents = append(rt.TopDependents, src)
		}
	}
	dep.Close()
	if err := dep.Err(); err != nil {
		return rt, err
	}

	// Governing decisions: records that name this file.
	dr, err := db.QueryContext(ctx, `
		SELECT title FROM decision_records
		 WHERE repository_id = ? AND (evidence_file = ? OR affected_files_json LIKE ?)
		 ORDER BY confidence DESC LIMIT 10`, repoID, fp, "%\""+fp+"\"%")
	if err != nil {
		return rt, err
	}
	for dr.Next() {
		var title string
		if err := dr.Scan(&title); err != nil {
			dr.Close()
			return rt, err
		}
		rt.Decisions = append(rt.Decisions, title)
	}
	dr.Close()
	if err := dr.Err(); err != nil {
		return rt, err
	}

	rt.Risk, rt.Reasons = deriveRisk(rt)
	return rt, nil
}

// deriveRisk turns the gathered signals into a level + human reasons.
func deriveRisk(rt RiskTarget) (string, []string) {
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
