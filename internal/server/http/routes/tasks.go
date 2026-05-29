package routes

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/persistence/settingsstore"
	"github.com/HundredAcreStudio/vor/internal/pipeline/tasks"
	"github.com/HundredAcreStudio/vor/internal/providerfactory"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountTasks wires the per-repo post-pipeline task endpoints under
// /api/repos/{repoID}.
//
//	GET .../tasks            — registered tasks + effective enablement
//	PUT .../tasks/{taskID}   — set a repo-scoped enable/disable ({"enabled":bool})
//
// Enablement is stored as the repo-scoped "tasks" setting (a JSON
// map[taskID]bool), resolved with the usual global→repo precedence in
// config.Resolve.
func MountTasks(r chi.Router, deps Deps) {
	r.Get("/tasks", listTasks(deps))
	r.Put("/tasks/{taskID}", putTask(deps))
}

type taskDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Default     bool   `json:"default"`
	Enabled     bool   `json:"enabled"`    // effective, after overrides
	Overridden  bool   `json:"overridden"` // explicitly set at this repo's scope
}

func listTasks(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		cfg, err := config.Resolve(r.Context(), deps.DB, repoID, config.LoadBootstrap())
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		// Which task IDs this repo explicitly overrides (vs. inheriting the
		// default / global) — lets the UI show "customised here".
		repoOverrides, err := readTaskOverrides(r, deps, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}

		provider, _ := providerfactory.Optional(cfg)
		out := make([]taskDTO, 0)
		for _, t := range tasks.Registered() {
			_, overridden := repoOverrides[t.ID()]
			out = append(out, taskDTO{
				ID:          t.ID(),
				Name:        t.Name(),
				Description: t.Description(),
				Default:     t.DefaultEnabled(),
				Enabled:     tasks.Enabled(t, cfg.Tasks),
				Overridden:  overridden,
			})
		}
		httpx.JSON(w, http.StatusOK, map[string]any{
			"tasks":              out,
			"providerConfigured": provider != nil,
		})
	}
}

func putTask(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		taskID := httpx.URLParam(r, "taskID")
		if _, ok := tasks.Get(taskID); !ok {
			httpx.BadRequest(w, "unknown task "+taskID)
			return
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			httpx.BadRequest(w, "could not read body")
			return
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			httpx.BadRequest(w, "body must be {\"enabled\": bool}")
			return
		}

		// Read-modify-write the repo-scoped "tasks" map so toggling one task
		// preserves the others.
		overrides, err := readTaskOverrides(r, deps, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		if overrides == nil {
			overrides = map[string]bool{}
		}
		overrides[taskID] = body.Enabled
		encoded, err := json.Marshal(overrides)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		if err := settingsstore.New(deps.DB).Set(r.Context(), repoID, config.KeyTasks, string(encoded)); err != nil {
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"id": taskID, "enabled": body.Enabled})
	}
}

// readTaskOverrides returns the repo-scoped task enablement map (just this
// repo's explicit settings, not merged with global). Empty when unset.
func readTaskOverrides(r *http.Request, deps Deps, repoID string) (map[string]bool, error) {
	raw, ok, err := settingsstore.New(deps.DB).Get(r.Context(), repoID, config.KeyTasks)
	if err != nil || !ok || raw == "" {
		return map[string]bool{}, err
	}
	var m map[string]bool
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]bool{}, nil // tolerate malformed; treat as no overrides
	}
	return m, nil
}
