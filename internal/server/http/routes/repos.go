package routes

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
	"github.com/HundredAcreStudio/vor/internal/server/registry"
)

// MountRepos wires the /api/repos endpoints (collection level) onto r.
// Per-repo endpoints live in other route files and mount under
// /api/repos/{repoID}.
func MountRepos(r chi.Router, deps Deps) {
	store := repos.New(deps.DB)
	r.Get("/", listRepos(store))
	// Daemon registry control.
	r.Get("/tracked", listTracked(deps))
	r.Post("/register", registerRepo(deps))
	r.Post("/unregister", unregisterRepo(deps))
}

// MountRepoDetail registers the single-repo endpoint as the index of the
// /api/repos/{repoID} subrouter. It lives on the subrouter (not as a bare
// /{repoID} on the collection router) because a sibling Route("/{repoID}",
// …) mount would otherwise shadow it, leaving GET /api/repos/{id} a 404.
func MountRepoDetail(r chi.Router, deps Deps) {
	store := repos.New(deps.DB)
	r.Get("/", getRepo(store))
	r.Delete("/", deleteRepo(deps))
	// Non-destructive maintenance triggers (dashboard Settings actions). Each
	// launches background work on the daemon and returns 202.
	r.Post("/reindex", triggerReindex(deps, "manual reindex", false, false))
	r.Post("/wiki/regenerate", triggerReindex(deps, "manual wiki regenerate", false, true))
	r.Post("/health/rescan", triggerReindex(deps, "manual biomarker rescan", true, false))
}

// triggerReindex returns a handler that kicks off a non-destructive background
// re-index of {repoID}. forceHealth forces a full biomarker recompute;
// forceWiki forces a full LLM wiki regeneration. Returns 202 Accepted once the
// work is launched, or 503 when no live watcher is running.
func triggerReindex(deps Deps, reason string, forceHealth, forceWiki bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Registrar == nil {
			httpx.Error(w, http.StatusServiceUnavailable, "reindex unavailable (no watcher running)")
			return
		}
		id := httpx.URLParam(r, "repoID") // canonicalized by ResolveRepoRef
		repo, err := deps.Registrar.Reindex(r.Context(), id, reason, forceHealth, forceWiki)
		if err != nil {
			if errors.Is(err, registry.ErrNoWatcher) {
				httpx.Error(w, http.StatusServiceUnavailable, "reindex unavailable (no watcher running)")
				return
			}
			httpx.BadRequest(w, err.Error())
			return
		}
		httpx.JSON(w, http.StatusAccepted, map[string]any{
			"status": "started",
			"repo":   repo,
		})
	}
}

// deleteRepo handles DELETE /api/repos/{repoID}: stop watching the repo and
// drop it and all its indexed data. Uses the live Registrar when present (so
// the daemon also stops its watcher); otherwise deletes straight from the DB.
func deleteRepo(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := httpx.URLParam(r, "repoID")
		if deps.Registrar != nil {
			repo, err := deps.Registrar.Delete(r.Context(), id)
			if err != nil {
				httpx.BadRequest(w, err.Error())
				return
			}
			httpx.JSON(w, http.StatusOK, repo)
			return
		}
		store := repos.New(deps.DB)
		if _, err := store.Get(r.Context(), id); err != nil {
			if httpx.IsNoRows(err) {
				httpx.NotFound(w, "repository")
				return
			}
			httpx.Internal(w, err)
			return
		}
		if err := store.Delete(r.Context(), id); err != nil {
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"id": id})
	}
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
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	LocalPath     string `json:"localPath"`
	DefaultBranch string `json:"defaultBranch"`
	HeadCommit    string `json:"headCommit,omitempty"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

func toRepoDTO(r repos.Repository, slug string) repoDTO {
	return repoDTO{
		ID:            r.ID,
		Slug:          slug,
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
		slugs := repos.UniqueSlugs(rows)
		out := make([]repoDTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, toRepoDTO(row, slugs[row.ID]))
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
		// Compute the slug consistently with the list/resolver.
		slug := repos.Slug(row.Name)
		if all, err := store.List(r.Context()); err == nil {
			if s, ok := repos.UniqueSlugs(all)[row.ID]; ok {
				slug = s
			}
		}
		httpx.JSON(w, http.StatusOK, toRepoDTO(*row, slug))
	}
}
