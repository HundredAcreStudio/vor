package routes

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

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
	r.Get("/risk/ownership", riskOwnership(deps))       // treemap cells (module|file)
	r.Get("/risk/contributors", riskContributors(deps)) // contributor co-authorship network
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
			Silos:          scalarCount(ctx, deps.DB, `SELECT COUNT(*) FROM git_metadata WHERE repository_id = ? AND primary_owner_commit_pct > 0.8 AND commit_count_90d > 0`, repoID),
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

// ---- ownership treemap ---------------------------------------------------

type treemapCell struct {
	Name     string  `json:"name"`     // module name, or file path
	Value    int     `json:"value"`    // size weight (file count for modules; activity for files)
	Files    int     `json:"files"`    // file count (1 for a file cell)
	Risk     float64 `json:"risk"`     // 0..1, drives colour (avg churn percentile)
	Hotspots int     `json:"hotspots"` // hotspot files within the cell
	Owner    string  `json:"owner"`    // dominant owner
}

// riskOwnership returns treemap cells aggregated by module (default) or by
// file (?by=file), sized and risk-coloured from git_metadata.
func riskOwnership(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		byFile := r.URL.Query().Get("by") == "file"

		rows, err := deps.DB.QueryContext(r.Context(),
			`SELECT file_path, is_hotspot, churn_percentile, commit_count_90d, COALESCE(primary_owner_name,'')
			 FROM git_metadata WHERE repository_id = ?`, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()

		type fileRow struct {
			path    string
			hotspot int
			churn   float64
			commits int
			owner   string
		}
		var files []fileRow
		for rows.Next() {
			var f fileRow
			if err := rows.Scan(&f.path, &f.hotspot, &f.churn, &f.commits, &f.owner); err != nil {
				httpx.Internal(w, err)
				return
			}
			files = append(files, f)
		}

		var cells []treemapCell
		if byFile {
			// Top files by recent activity; size = commits, colour = churn.
			sort.Slice(files, func(i, j int) bool { return files[i].commits > files[j].commits })
			for i, f := range files {
				if i >= 200 {
					break
				}
				cells = append(cells, treemapCell{
					Name: f.path, Value: maxInt(f.commits, 1), Files: 1,
					Risk: f.churn, Hotspots: f.hotspot, Owner: f.owner,
				})
			}
		} else {
			// Aggregate by top-level module (first path segment).
			type agg struct {
				files, hotspots, commits int
				churnSum                 float64
				owners                   map[string]int
			}
			byMod := map[string]*agg{}
			for _, f := range files {
				mod := topSegment(f.path)
				a := byMod[mod]
				if a == nil {
					a = &agg{owners: map[string]int{}}
					byMod[mod] = a
				}
				a.files++
				a.hotspots += f.hotspot
				a.commits += f.commits
				a.churnSum += f.churn
				if f.owner != "" {
					a.owners[f.owner]++
				}
			}
			for name, a := range byMod {
				risk := 0.0
				if a.files > 0 {
					risk = a.churnSum / float64(a.files)
				}
				cells = append(cells, treemapCell{
					Name: name, Value: a.files, Files: a.files,
					Risk: risk, Hotspots: a.hotspots, Owner: topKey(a.owners),
				})
			}
			sort.Slice(cells, func(i, j int) bool { return cells[i].Value > cells[j].Value })
		}

		mode := "module"
		if byFile {
			mode = "file"
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"by": mode, "cells": cells})
	}
}

// ---- contributor network -------------------------------------------------

type contribNode struct {
	Name    string `json:"name"`
	Files   int    `json:"files"`
	Commits int    `json:"commits"`
}

type contribEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Weight int    `json:"weight"` // files both authors touched
}

// riskContributors builds a co-authorship network from per-file top authors:
// nodes are contributors (file/commit counts), edges connect authors who
// worked on the same files (weighted by shared files).
func riskContributors(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		rows, err := deps.DB.QueryContext(r.Context(),
			`SELECT COALESCE(top_authors_json,'') FROM git_metadata WHERE repository_id = ?`, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()

		// Keys match git.AuthorShare as marshalled into top_authors_json
		// (exported field names, no json tags).
		type author struct {
			Name        string
			CommitCount int
		}
		nodeFiles := map[string]int{}
		nodeCommits := map[string]int{}
		edgeW := map[string]int{} // "a\x00b" (a<b) -> shared files
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				continue
			}
			if raw == "" {
				continue
			}
			var authors []author
			if json.Unmarshal([]byte(raw), &authors) != nil {
				continue
			}
			names := make([]string, 0, len(authors))
			for _, a := range authors {
				if a.Name == "" {
					continue
				}
				nodeFiles[a.Name]++
				nodeCommits[a.Name] += a.CommitCount
				names = append(names, a.Name)
			}
			for i := 0; i < len(names); i++ {
				for j := i + 1; j < len(names); j++ {
					a, b := names[i], names[j]
					if a > b {
						a, b = b, a
					}
					edgeW[a+"\x00"+b]++
				}
			}
		}

		nodes := make([]contribNode, 0, len(nodeFiles))
		for name, files := range nodeFiles {
			nodes = append(nodes, contribNode{Name: name, Files: files, Commits: nodeCommits[name]})
		}
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].Files > nodes[j].Files })
		if len(nodes) > 40 { // cap to keep the graph legible
			nodes = nodes[:40]
		}
		keep := map[string]bool{}
		for _, n := range nodes {
			keep[n.Name] = true
		}
		edges := make([]contribEdge, 0, len(edgeW))
		for k, wgt := range edgeW {
			parts := strings.SplitN(k, "\x00", 2)
			if len(parts) != 2 || !keep[parts[0]] || !keep[parts[1]] {
				continue
			}
			edges = append(edges, contribEdge{Source: parts[0], Target: parts[1], Weight: wgt})
		}
		sort.Slice(edges, func(i, j int) bool { return edges[i].Weight > edges[j].Weight })
		if len(edges) > 200 {
			edges = edges[:200]
		}

		httpx.JSON(w, http.StatusOK, map[string]any{"nodes": nodes, "edges": edges})
	}
}

// ---- helpers --------------------------------------------------------------

// topSegment returns the first path segment ("internal/foo/bar.go" -> "internal";
// "main.go" -> "(root)").
func topSegment(path string) string {
	if i := strings.IndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return "(root)"
}

// topKey returns the map key with the highest count, or "".
func topKey(m map[string]int) string {
	best, bestN := "", 0
	for k, n := range m {
		if n > bestN {
			best, bestN = k, n
		}
	}
	return best
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// scalarCount runs a COUNT(*) query and returns 0 on any error (best-effort:
// a missing/empty table shouldn't break the whole risk roll-up).
func scalarCount(ctx context.Context, db *sql.DB, query, repoID string) int {
	var n int
	_ = db.QueryRowContext(ctx, query, repoID).Scan(&n)
	return n
}
