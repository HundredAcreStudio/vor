package routes

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/persistence/pipelinestore"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
	"github.com/HundredAcreStudio/vor/internal/workspace"
)

// MountWorkspace registers workspace-level routes. These are only
// useful when the daemon was started against a workspace root (so
// .vor/workspace.json exists at that path); when not, the
// endpoints return an empty payload with a hint.
//
// The workspace root is discovered lazily from each request via the
// `root` query parameter, defaulting to the daemon's working dir.
// Lazy discovery avoids forcing the HTTP layer to know about the
// CLI's --workspace-root flag.
func MountWorkspace(r chi.Router, deps Deps) {
	r.Get("/workspace", listWorkspaceRepos(deps))
	r.Get("/workspace/co-changes", listWorkspaceCoChanges(deps))
}

type workspaceRepoDTO struct {
	Alias         string `json:"alias"`
	Path          string `json:"path"`
	RepositoryID  string `json:"repositoryId,omitempty"`
	IsDefault     bool   `json:"isDefault"`
	LatestOverall string `json:"latestRunOverall,omitempty"`
}

// listWorkspaceRepos returns the registered repos + their indexed
// status from the shared DB. The workspace root comes from ?root=PATH;
// defaults to the daemon's cwd.
func listWorkspaceRepos(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		root = resolveWorkspaceRoot(root)

		state, err := workspace.Load(root)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		if len(state.Repos) == 0 {
			httpx.JSON(w, http.StatusOK, map[string]any{
				"root":  root,
				"repos": []workspaceRepoDTO{},
				"hint":  "no repos registered — use `vor workspace add PATH`",
			})
			return
		}
		out := make([]workspaceRepoDTO, 0, len(state.Repos))
		for _, e := range state.Sorted() {
			entry := workspaceRepoDTO{
				Alias:     e.Alias,
				Path:      e.Path,
				IsDefault: e.Alias == state.DefaultAlias,
			}
			if id, err := lookupRepoIDByPath(r.Context(), deps.DB, e.Path); err == nil {
				entry.RepositoryID = id
				if latest, _ := pipelinestore.New(deps.DB).LatestRun(r.Context(), id); latest != nil {
					entry.LatestOverall = latest.Overall
				}
			}
			out = append(out, entry)
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"root":         root,
			"repos":        out,
			"defaultAlias": state.DefaultAlias,
		})
	}
}

// listWorkspaceCoChanges returns the cached cross-repo co-change
// report (workspace.LoadReport). Empty when no report exists yet —
// callers should run `vor workspace co-changes --refresh`.
func listWorkspaceCoChanges(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		root = resolveWorkspaceRoot(root)
		report, err := workspace.LoadReport(root)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		if report == nil {
			httpx.JSON(w, http.StatusOK, map[string]any{
				"root":  root,
				"pairs": []any{},
				"hint":  "no cached report — run `vor workspace co-changes --refresh`",
			})
			return
		}
		httpx.JSON(w, http.StatusOK, report)
	}
}

// resolveWorkspaceRoot picks the directory holding workspace.json.
// Uses the ?root= query param when set; otherwise auto-detects by
// walking up from the daemon's cwd; falls back to cwd unchanged.
func resolveWorkspaceRoot(hint string) string {
	if hint != "" {
		abs, err := filepath.Abs(hint)
		if err == nil {
			return abs
		}
		return hint
	}
	cwd, _ := os.Getwd()
	if found, _ := workspace.FindRoot(cwd); found != "" {
		return found
	}
	return cwd
}

// lookupRepoIDByPath does a read-only lookup — does NOT create a
// repository row on miss, unlike repos.EnsureByLocalPath. Matches the
// MCP server's resolver semantics.
func lookupRepoIDByPath(ctx context.Context, db *sql.DB, absPath string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM repositories WHERE local_path = ?`, absPath).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("not indexed")
		}
		return "", err
	}
	// Keep repos import alive for symmetry with the rest of the
	// routes package; this helper might use the store directly later.
	_ = repos.New(db)
	return id, nil
}
