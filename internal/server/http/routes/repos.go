package routes

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountRepos wires the /api/repos endpoints (collection level) onto r.
// Per-repo endpoints live in other route files and mount under
// /api/repos/{repoID}.
func MountRepos(r chi.Router, deps Deps) {
	store := repos.New(deps.DB)
	r.Get("/", listRepos(store))
	// Daemon registry control. Static segments register before the
	// /{repoID} wildcard so they take precedence.
	r.Get("/tracked", listTracked(deps))
	r.Post("/register", registerRepo(deps))
	r.Post("/unregister", unregisterRepo(deps))
	r.Get("/{repoID}", getRepo(store))
}

// registerRepo handles POST /api/repos/register {path, ephemeral}: track a
// repo and start watching it on the live daemon.
func registerRepo(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Registrar == nil {
			httpx.Error(w, http.StatusServiceUnavailable, "registration unavailable (no watcher running)")
			return
		}
		var body struct {
			Path      string `json:"path"`
			Ephemeral bool   `json:"ephemeral"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
			httpx.BadRequest(w, `body must be {"path": "...", "ephemeral": bool}`)
			return
		}
		repo, err := deps.Registrar.Register(r.Context(), body.Path, body.Ephemeral)
		if err != nil {
			httpx.BadRequest(w, err.Error())
			return
		}
		httpx.JSON(w, http.StatusOK, repo)
	}
}

// unregisterRepo handles POST /api/repos/unregister {repo}: stop watching a
// repo (id or path); ephemeral repos are purged.
func unregisterRepo(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Registrar == nil {
			httpx.Error(w, http.StatusServiceUnavailable, "registration unavailable (no watcher running)")
			return
		}
		var body struct {
			Repo string `json:"repo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Repo == "" {
			httpx.BadRequest(w, `body must be {"repo": "<id or path>"}`)
			return
		}
		repo, err := deps.Registrar.Unregister(r.Context(), body.Repo)
		if err != nil {
			httpx.BadRequest(w, err.Error())
			return
		}
		httpx.JSON(w, http.StatusOK, repo)
	}
}

// listTracked handles GET /api/repos/tracked: the daemon's current watch set.
func listTracked(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Registrar == nil {
			httpx.JSON(w, http.StatusOK, []any{})
			return
		}
		list, err := deps.Registrar.ListTracked(r.Context())
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, list)
	}
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
