package routes

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/repowise-dev/repowise-go/internal/server/http/httpx"
)

// MountPages registers /pages routes under /api/repos/{repoID}.
//
//	GET .../pages?kind=&stale=&limit=
//	GET .../pages/show?path=internal/foo.go&kind=file_overview
//
// The "show" variant lives under a child path rather than a wildcard
// param because chi's wildcard handling doesn't compose cleanly with
// existing /pages collection routes.
func MountPages(r chi.Router, deps Deps) {
	r.Get("/pages", listPages(deps))
	r.Get("/pages/show", showPage(deps))
}

type pageSummaryDTO struct {
	ID           string `json:"id"`
	PageType     string `json:"pageType"`
	TargetPath   string `json:"targetPath"`
	Title        string `json:"title"`
	Summary      string `json:"summary,omitempty"`
	Version      int    `json:"version"`
	Freshness    string `json:"freshness"`
	ModelName    string `json:"modelName"`
	ProviderName string `json:"providerName"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
	CachedTokens int    `json:"cachedTokens"`
	UpdatedAt    string `json:"updatedAt"`
}

type pageContentDTO struct {
	pageSummaryDTO
	Content    string `json:"content"`
	SourceHash string `json:"sourceHash"`
}

func listPages(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		kind := r.URL.Query().Get("kind")
		stale := r.URL.Query().Get("stale") == "1" ||
			strings.EqualFold(r.URL.Query().Get("stale"), "true")
		limit := httpx.ParseIntQuery(r, "limit", 100)
		if limit > 500 {
			limit = 500
		}

		query := `SELECT id, page_type, target_path, title, summary,
		                 version, freshness_status, model_name, provider_name,
		                 input_tokens, output_tokens, cached_tokens, updated_at
		          FROM wiki_pages WHERE repository_id = ?`
		args := []any{repoID}
		if kind != "" {
			query += " AND page_type = ?"
			args = append(args, kind)
		}
		if stale {
			query += " AND freshness_status != 'fresh'"
		}
		query += " ORDER BY target_path LIMIT ?"
		args = append(args, limit)

		rows, err := deps.DB.QueryContext(r.Context(), query, args...)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()
		out := make([]pageSummaryDTO, 0, limit)
		for rows.Next() {
			var p pageSummaryDTO
			var updated sql.NullTime
			if err := rows.Scan(&p.ID, &p.PageType, &p.TargetPath, &p.Title, &p.Summary,
				&p.Version, &p.Freshness, &p.ModelName, &p.ProviderName,
				&p.InputTokens, &p.OutputTokens, &p.CachedTokens, &updated); err != nil {
				httpx.Internal(w, err)
				return
			}
			if updated.Valid {
				p.UpdatedAt = updated.Time.UTC().Format("2006-01-02T15:04:05Z")
			}
			out = append(out, p)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"pages": out,
			"limit": limit,
		})
	}
}

func showPage(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		target := r.URL.Query().Get("path")
		kind := r.URL.Query().Get("kind")
		if target == "" {
			httpx.BadRequest(w, "missing required query param 'path'")
			return
		}
		if kind == "" {
			kind = "file_overview"
		}

		var p pageContentDTO
		var updated sql.NullTime
		err := deps.DB.QueryRowContext(r.Context(), `
			SELECT id, page_type, target_path, title, summary,
			       version, freshness_status, model_name, provider_name,
			       input_tokens, output_tokens, cached_tokens, updated_at,
			       content, source_hash
			FROM wiki_pages
			WHERE repository_id = ? AND page_type = ? AND target_path = ?
		`, repoID, kind, target).Scan(
			&p.ID, &p.PageType, &p.TargetPath, &p.Title, &p.Summary,
			&p.Version, &p.Freshness, &p.ModelName, &p.ProviderName,
			&p.InputTokens, &p.OutputTokens, &p.CachedTokens, &updated,
			&p.Content, &p.SourceHash,
		)
		if errors.Is(err, sql.ErrNoRows) {
			httpx.NotFound(w, "page")
			return
		}
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		if updated.Valid {
			p.UpdatedAt = updated.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		httpx.JSON(w, http.StatusOK, p)
	}
}
