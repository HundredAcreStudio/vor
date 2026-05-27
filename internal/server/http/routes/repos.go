package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/server/http/httpx"
)

// MountRepos wires the /api/repos endpoints (collection level) onto r.
// Per-repo endpoints live in other route files and mount under
// /api/repos/{repoID}.
func MountRepos(r chi.Router, deps Deps) {
	store := repos.New(deps.DB)
	r.Get("/", listRepos(store))
	r.Get("/{repoID}", getRepo(store))
}

type repoDTO struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	LocalPath     string `json:"localPath"`
	DefaultBranch string `json:"defaultBranch"`
	HeadCommit    string `json:"headCommit,omitempty"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

func toRepoDTO(r repos.Repository) repoDTO {
	return repoDTO{
		ID:            r.ID,
		Name:          r.Name,
		URL:           r.URL,
		LocalPath:     r.LocalPath,
		DefaultBranch: r.DefaultBranch,
		HeadCommit:    r.HeadCommit,
		CreatedAt:     r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:     r.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func listRepos(store *repos.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := store.List(r.Context())
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		out := make([]repoDTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, toRepoDTO(row))
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"repos": out})
	}
}

func getRepo(store *repos.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := httpx.URLParam(r, "repoID")
		row, err := store.Get(r.Context(), id)
		if err != nil {
			if httpx.IsNoRows(err) {
				httpx.NotFound(w, "repository")
				return
			}
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, toRepoDTO(*row))
	}
}
