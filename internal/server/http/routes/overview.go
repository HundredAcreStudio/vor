package routes

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/persistence/healthstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountOverview wires GET /api/overview: a single roll-up the dashboard
// landing page uses to render one card per repository. It exists so the
// browser makes one request instead of fanning out per-repo health/graph
// calls (N+1) just to draw the list.
func MountOverview(r chi.Router, deps Deps) {
	r.Get("/overview", overview(deps))
}

type repoOverviewDTO struct {
	ID           string  `json:"id"`
	Slug         string  `json:"slug"`
	Name         string  `json:"name"`
	LocalPath    string  `json:"localPath"`
	HeadCommit   string  `json:"headCommit,omitempty"`
	LastIndexed  string  `json:"lastIndexed"`
	FileCount    int     `json:"fileCount"`
	SymbolCount  int     `json:"symbolCount"`
	HealthAvg    float64 `json:"healthAvg"`
	FindingCount int     `json:"findingCount"`
}

func overview(deps Deps) http.HandlerFunc {
	repoStore := repos.New(deps.DB)
	healthStore := healthstore.New(deps.DB)
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		rows, err := repoStore.List(ctx)
		if err != nil {
			httpx.Internal(w, err)
			return
		}

		slugs := repos.UniqueSlugs(rows)
		out := make([]repoOverviewDTO, 0, len(rows))
		for _, repo := range rows {
			files, symbols, err := nodeCounts(ctx, deps.DB, repo.ID)
			if err != nil {
				httpx.Internal(w, err)
				return
			}
			avg, err := healthStore.AverageScore(ctx, repo.ID)
			if err != nil {
				httpx.Internal(w, err)
				return
			}
			findings, err := healthStore.CountFindings(ctx, repo.ID)
			if err != nil {
				httpx.Internal(w, err)
				return
			}
			out = append(out, repoOverviewDTO{
				ID:           repo.ID,
				Slug:         slugs[repo.ID],
				Name:         repo.Name,
				LocalPath:    repo.LocalPath,
				HeadCommit:   repo.HeadCommit,
				LastIndexed:  repo.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
				FileCount:    files,
				SymbolCount:  symbols,
				HealthAvg:    avg,
				FindingCount: findings,
			})
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"repos": out})
	}
}

// nodeCounts returns (files, symbols) from graph_nodes for one repo. File
// nodes carry node_type='file'; everything else is a symbol. Both come from
// one scan so the counts are mutually consistent.
func nodeCounts(ctx context.Context, db *sql.DB, repoID string) (files, symbols int, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT
		   COALESCE(SUM(CASE WHEN node_type = 'file' THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN node_type <> 'file' THEN 1 ELSE 0 END), 0)
		 FROM graph_nodes WHERE repository_id = ?`, repoID).Scan(&files, &symbols)
	return files, symbols, err
}
