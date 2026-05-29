package routes

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountInsights wires the overview's structural + git-insight endpoints under
// /api/repos/{repoID}:
//
//	GET .../communities   — graph clusters (community detection) by size
//	GET .../entry-points  — indexed entry-point files
//	GET .../git-insights  — bus factor, churn distribution, top contributors
func MountInsights(r chi.Router, deps Deps) {
	r.Get("/communities", communities(deps))
	r.Get("/entry-points", entryPoints(deps))
	r.Get("/git-insights", gitInsights(deps))
	r.Get("/dependency-matrix", dependencyMatrix(deps))
}

// dependencyMatrix returns a module×module import-density matrix. Modules are
// the first two path segments of each file (e.g. internal/server, ui/src), so
// the matrix is coarse enough to read. Cell [i][j] counts import edges from a
// file in module i to a file in module j.
func dependencyMatrix(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		// For file nodes the node_id IS the file path, and import edges
		// connect file→file, so the edge endpoints are paths directly.
		rows, err := deps.DB.QueryContext(r.Context(), `
			SELECT source_node_id, target_node_id
			  FROM graph_edges
			 WHERE repository_id = ? AND edge_type = 'imports'`, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()

		pairCount := map[[2]string]int{}
		moduleTotal := map[string]int{}
		for rows.Next() {
			var sp, tp string
			if err := rows.Scan(&sp, &tp); err != nil {
				httpx.Internal(w, err)
				return
			}
			if sp == "" || tp == "" {
				continue
			}
			a, b := moduleKey(sp), moduleKey(tp)
			pairCount[[2]string{a, b}]++
			moduleTotal[a]++
			moduleTotal[b]++
		}
		if err := rows.Err(); err != nil {
			httpx.Internal(w, err)
			return
		}

		// Keep the most-connected modules so the grid stays legible.
		mods := make([]string, 0, len(moduleTotal))
		for m := range moduleTotal {
			mods = append(mods, m)
		}
		sort.Slice(mods, func(i, j int) bool { return moduleTotal[mods[i]] > moduleTotal[mods[j]] })
		if len(mods) > 16 {
			mods = mods[:16]
		}
		sort.Strings(mods)

		idx := map[string]int{}
		for i, m := range mods {
			idx[m] = i
		}
		cells := make([][]int, len(mods))
		for i := range cells {
			cells[i] = make([]int, len(mods))
		}
		for pair, n := range pairCount {
			i, iok := idx[pair[0]]
			j, jok := idx[pair[1]]
			if iok && jok {
				cells[i][j] += n
			}
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"modules": mods, "cells": cells})
	}
}

// moduleKey collapses a file path to its first two segments (or one).
func moduleKey(path string) string {
	parts := strings.SplitN(path, "/", 3)
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

type communityDTO struct {
	CommunityID int      `json:"communityId"`
	Label       string   `json:"label"`
	Size        int      `json:"size"`
	Top         []string `json:"top"`
}

func communities(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		repoID := httpx.URLParam(r, "repoID")
		rows, err := deps.DB.QueryContext(ctx, `
			SELECT community_id, COUNT(*) AS n
			  FROM graph_nodes
			 WHERE repository_id = ? AND node_type = 'file'
			 GROUP BY community_id
			 ORDER BY n DESC LIMIT 20`, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()
		out := make([]communityDTO, 0, 20)
		for rows.Next() {
			var c communityDTO
			if err := rows.Scan(&c.CommunityID, &c.Size); err != nil {
				httpx.Internal(w, err)
				return
			}
			out = append(out, c)
		}
		if err := rows.Err(); err != nil {
			httpx.Internal(w, err)
			return
		}
		// Label + top members per community. Community count is small, so a
		// follow-up query each keeps the grouping query simple.
		for i := range out {
			members, err := communityFiles(ctx, deps.DB, repoID, out[i].CommunityID, 5)
			if err != nil {
				httpx.Internal(w, err)
				return
			}
			out[i].Top = members
			out[i].Label = dominantDir(members)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"communities": out})
	}
}

func communityFiles(ctx context.Context, db *sql.DB, repoID string, cid, limit int) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(file_path, node_id)
		  FROM graph_nodes
		 WHERE repository_id = ? AND node_type = 'file' AND community_id = ?
		 ORDER BY pagerank DESC LIMIT ?`, repoID, cid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func entryPoints(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := deps.DB.QueryContext(r.Context(), `
			SELECT COALESCE(file_path, node_id), COALESCE(language, ''), pagerank
			  FROM graph_nodes
			 WHERE repository_id = ? AND is_entry_point = 1
			 ORDER BY pagerank DESC LIMIT 100`, httpx.URLParam(r, "repoID"))
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()
		type epDTO struct {
			Path     string  `json:"path"`
			Language string  `json:"language,omitempty"`
			PageRank float64 `json:"pagerank"`
		}
		out := make([]epDTO, 0, 32)
		for rows.Next() {
			var e epDTO
			if err := rows.Scan(&e.Path, &e.Language, &e.PageRank); err != nil {
				httpx.Internal(w, err)
				return
			}
			out = append(out, e)
		}
		if err := rows.Err(); err != nil {
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"entryPoints": out})
	}
}

func gitInsights(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		repoID := httpx.URLParam(r, "repoID")

		// Bus-factor distribution: Safe (≥3 contributors), Warning (2),
		// Risk (≤1) — plus the highest-risk files.
		var safe, warning, risk, total int
		if err := deps.DB.QueryRowContext(ctx, `
			SELECT
			  COALESCE(SUM(CASE WHEN bus_factor >= 3 THEN 1 ELSE 0 END), 0),
			  COALESCE(SUM(CASE WHEN bus_factor = 2 THEN 1 ELSE 0 END), 0),
			  COALESCE(SUM(CASE WHEN bus_factor <= 1 THEN 1 ELSE 0 END), 0),
			  COUNT(*)
			FROM git_metadata WHERE repository_id = ?`, repoID).Scan(&safe, &warning, &risk, &total); err != nil {
			httpx.Internal(w, err)
			return
		}
		riskFiles, err := busFactorRiskFiles(ctx, deps.DB, repoID, 5)
		if err != nil {
			httpx.Internal(w, err)
			return
		}

		// Churn distribution: files bucketed by churn percentile.
		type bucket struct {
			Label string `json:"label"`
			Count int    `json:"count"`
		}
		buckets := []bucket{{Label: "0–20"}, {Label: "20–40"}, {Label: "40–60"}, {Label: "60–80"}, {Label: "80–100"}}
		crows, err := deps.DB.QueryContext(ctx,
			`SELECT churn_percentile FROM git_metadata WHERE repository_id = ?`, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		for crows.Next() {
			var p float64
			if err := crows.Scan(&p); err != nil {
				crows.Close()
				httpx.Internal(w, err)
				return
			}
			idx := int(p * 5)
			if idx > 4 {
				idx = 4
			}
			if idx < 0 {
				idx = 0
			}
			buckets[idx].Count++
		}
		crows.Close()
		if err := crows.Err(); err != nil {
			httpx.Internal(w, err)
			return
		}

		// Top contributors: aggregate top_authors_json across files.
		contributors, err := topContributors(ctx, deps.DB, repoID, 8)
		if err != nil {
			httpx.Internal(w, err)
			return
		}

		httpx.JSON(w, http.StatusOK, map[string]any{
			"busFactor": map[string]any{
				"safe": safe, "warning": warning, "risk": risk, "total": total,
				"riskFiles": riskFiles,
			},
			"churnBuckets": buckets,
			"contributors": contributors,
		})
	}
}

type riskFileDTO struct {
	Path         string `json:"path"`
	Contributors int    `json:"contributors"`
}

// busFactorRiskFiles returns the lowest-bus-factor files (most at risk),
// ordered by churn so the riskiest *and* most active surface first.
func busFactorRiskFiles(ctx context.Context, db *sql.DB, repoID string, limit int) ([]riskFileDTO, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT file_path, contributor_count
		  FROM git_metadata
		 WHERE repository_id = ? AND bus_factor <= 1
		 ORDER BY commit_count_90d DESC, churn_percentile DESC
		 LIMIT ?`, repoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]riskFileDTO, 0, limit)
	for rows.Next() {
		var f riskFileDTO
		if err := rows.Scan(&f.Path, &f.Contributors); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

type contributorDTO struct {
	Name    string `json:"name"`
	Commits int    `json:"commits"`
}

func topContributors(ctx context.Context, db *sql.DB, repoID string, limit int) ([]contributorDTO, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT top_authors_json FROM git_metadata WHERE repository_id = ?`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	totals := map[string]int{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var authors []struct {
			Name        string `json:"Name"`
			CommitCount int    `json:"CommitCount"`
		}
		if raw == "" || json.Unmarshal([]byte(raw), &authors) != nil {
			continue
		}
		for _, a := range authors {
			if a.Name != "" {
				totals[a.Name] += a.CommitCount
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]contributorDTO, 0, len(totals))
	for name, n := range totals {
		out = append(out, contributorDTO{Name: name, Commits: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Commits > out[j].Commits })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// dominantDir returns the most common top-level directory across paths, used
// to label a community (e.g. "internal", "ui"). Falls back to "—".
func dominantDir(paths []string) string {
	counts := map[string]int{}
	for _, p := range paths {
		seg := p
		if i := strings.IndexByte(p, '/'); i >= 0 {
			seg = p[:i]
		}
		counts[seg]++
	}
	best, bestN := "—", 0
	for seg, n := range counts {
		if n > bestN {
			best, bestN = seg, n
		}
	}
	return best
}
