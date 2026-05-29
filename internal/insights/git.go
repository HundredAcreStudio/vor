package insights

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
)

// GitInsights bundles the repo-level git aggregates for the dashboard's Git
// insights section.
type GitInsights struct {
	BusFactor    BusFactor      `json:"busFactor"`
	ChurnBuckets []ChurnBucket  `json:"churnBuckets"`
	Contributors []Contributor  `json:"contributors"`
}

type BusFactor struct {
	Safe      int        `json:"safe"`
	Warning   int        `json:"warning"`
	Risk      int        `json:"risk"`
	Total     int        `json:"total"`
	RiskFiles []RiskFile `json:"riskFiles"`
}

type RiskFile struct {
	Path         string `json:"path"`
	Contributors int    `json:"contributors"`
}

type ChurnBucket struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type Contributor struct {
	Name    string `json:"name"`
	Commits int    `json:"commits"`
}

// GitInsightsFor computes the bus-factor distribution, churn distribution, and
// top contributors for a repo.
func GitInsightsFor(ctx context.Context, db *sql.DB, repoID string) (GitInsights, error) {
	var gi GitInsights

	if err := db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(CASE WHEN bus_factor >= 3 THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN bus_factor = 2 THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN bus_factor <= 1 THEN 1 ELSE 0 END), 0),
		  COUNT(*)
		FROM git_metadata WHERE repository_id = ?`, repoID).
		Scan(&gi.BusFactor.Safe, &gi.BusFactor.Warning, &gi.BusFactor.Risk, &gi.BusFactor.Total); err != nil {
		return gi, err
	}
	riskFiles, err := busFactorRiskFiles(ctx, db, repoID, 5)
	if err != nil {
		return gi, err
	}
	gi.BusFactor.RiskFiles = riskFiles

	gi.ChurnBuckets = []ChurnBucket{
		{Label: "0–20"}, {Label: "20–40"}, {Label: "40–60"}, {Label: "60–80"}, {Label: "80–100"},
	}
	crows, err := db.QueryContext(ctx,
		`SELECT churn_percentile FROM git_metadata WHERE repository_id = ?`, repoID)
	if err != nil {
		return gi, err
	}
	for crows.Next() {
		var p float64
		if err := crows.Scan(&p); err != nil {
			crows.Close()
			return gi, err
		}
		idx := int(p * 5)
		if idx > 4 {
			idx = 4
		}
		if idx < 0 {
			idx = 0
		}
		gi.ChurnBuckets[idx].Count++
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return gi, err
	}

	contributors, err := topContributors(ctx, db, repoID, 8)
	if err != nil {
		return gi, err
	}
	gi.Contributors = contributors
	return gi, nil
}

func busFactorRiskFiles(ctx context.Context, db *sql.DB, repoID string, limit int) ([]RiskFile, error) {
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
	out := make([]RiskFile, 0, limit)
	for rows.Next() {
		var f RiskFile
		if err := rows.Scan(&f.Path, &f.Contributors); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func topContributors(ctx context.Context, db *sql.DB, repoID string, limit int) ([]Contributor, error) {
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
	out := make([]Contributor, 0, len(totals))
	for name, n := range totals {
		out = append(out, Contributor{Name: name, Commits: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Commits > out[j].Commits })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CommitCategory is one repo-level commit-category tally.
type CommitCategory struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// CommitCategoriesResult is the categories plus their total.
type CommitCategoriesResult struct {
	Categories []CommitCategory `json:"categories"`
	Total      int              `json:"total"`
}

// CommitCategories returns the repo-level commit-category tally.
func CommitCategories(ctx context.Context, db *sql.DB, repoID string) (CommitCategoriesResult, error) {
	var res CommitCategoriesResult
	res.Categories = []CommitCategory{}
	rows, err := db.QueryContext(ctx,
		`SELECT category, count FROM commit_categories
		  WHERE repository_id = ? ORDER BY count DESC`, repoID)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var c CommitCategory
		if err := rows.Scan(&c.Category, &c.Count); err != nil {
			return res, err
		}
		res.Categories = append(res.Categories, c)
		res.Total += c.Count
	}
	return res, rows.Err()
}
