package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// ResolveRepoRef is middleware for the /api/repos/{repoID} subrouter. The
// {repoID} path segment may be a repository id OR a human-readable slug; this
// resolves it to the canonical id and rewrites the route param in place, so
// every downstream handler keeps reading httpx.URLParam(r, "repoID") and gets
// the id — no per-handler changes. 404s when nothing matches.
func ResolveRepoRef(deps Deps) func(http.Handler) http.Handler {
	store := repos.New(deps.DB)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ref := httpx.URLParam(r, "repoID")
			repo, err := store.ResolveRef(r.Context(), ref)
			if err != nil || repo == nil {
				httpx.NotFound(w, "repository")
				return
			}
			if repo.ID != ref {
				if rc := chi.RouteContext(r.Context()); rc != nil {
					for i, k := range rc.URLParams.Keys {
						if k == "repoID" {
							rc.URLParams.Values[i] = repo.ID
						}
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
