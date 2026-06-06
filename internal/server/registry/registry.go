// Package registry is the serve daemon's repo registration service: the
// shared logic behind the REST endpoints, the MCP tools, and the CLI's
// register/unregister commands. It is the single place that reconciles the
// DB's tracked set with the live filesystem watcher.
//
// Register marks a repo tracked in the DB and starts watching it.
// Unregister stops watching and — for ephemeral repos (e.g. agent
// worktrees) — purges their indexed data; durable repos keep their index.
package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

// ErrNoWatcher is returned by Reindex when the registrar has no live watcher
// (e.g. a CLI mutating the DB with no running daemon), so there's nothing to
// run the background work. The HTTP layer maps it to 503.
var ErrNoWatcher = errors.New("registry: no live watcher")

// Tracker is the slice of the autoindex watcher the registrar drives.
// Defined here (not imported) so registry stays decoupled from autoindex.
type Tracker interface {
	Track(repoID, root string)
	Untrack(repoID string)
	// Reindex triggers a non-destructive background re-index of repoID.
	// forceHealth forces a full biomarker recompute; forceWiki forces a full
	// LLM wiki regeneration. Primitive args keep registry decoupled from the
	// autoindex package's option types.
	Reindex(repoID, reason string, forceHealth, forceWiki bool) error
}

// Repo is the registrar's DTO for a tracked repository.
type Repo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Ephemeral bool   `json:"ephemeral"`
}

// Registrar reconciles the DB tracked set with the live watcher.
type Registrar struct {
	db      *sql.DB
	tracker Tracker
	logger  *slog.Logger
}

// New builds a Registrar. tracker may be nil (e.g. a CLI mutating the DB
// with no live daemon); registration then only flips DB state and the next
// daemon start picks it up.
func New(db *sql.DB, tracker Tracker, logger *slog.Logger) *Registrar {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registrar{db: db, tracker: tracker, logger: logger}
}

// Register marks the repo at path tracked (creating its row if needed) and
// starts watching it. ephemeral marks it disposable so Unregister purges
// its data. Idempotent.
func (r *Registrar) Register(ctx context.Context, path string, ephemeral bool) (Repo, error) {
	abs, err := absPath(path)
	if err != nil {
		return Repo{}, err
	}
	if fi, statErr := os.Stat(abs); statErr != nil || !fi.IsDir() {
		return Repo{}, fmt.Errorf("not a directory: %s", abs)
	}
	store := repos.New(r.db)
	row, err := store.EnsureByLocalPath(ctx, abs, "")
	if err != nil {
		return Repo{}, fmt.Errorf("ensure repo: %w", err)
	}
	if err := store.SetTracked(ctx, row.ID, true, ephemeral); err != nil {
		return Repo{}, fmt.Errorf("mark tracked: %w", err)
	}
	if r.tracker != nil {
		r.tracker.Track(row.ID, abs)
	}
	r.logger.Info("registry: registered", "repo", row.ID, "path", abs, "ephemeral", ephemeral)
	return Repo{ID: row.ID, Name: row.Name, Path: abs, Ephemeral: ephemeral}, nil
}

// Unregister stops watching the repo identified by spec (a repository id or
// a local path). An ephemeral repo's indexed data is purged (cascade
// delete); a durable repo keeps its index and is only untracked.
func (r *Registrar) Unregister(ctx context.Context, spec string) (Repo, error) {
	store := repos.New(r.db)
	row, err := r.resolve(ctx, store, spec)
	if err != nil {
		return Repo{}, err
	}
	out := Repo{ID: row.ID, Name: row.Name, Path: row.LocalPath, Ephemeral: row.Ephemeral}

	if r.tracker != nil {
		r.tracker.Untrack(row.ID)
	}
	if row.Ephemeral {
		if err := store.Delete(ctx, row.ID); err != nil {
			return out, fmt.Errorf("purge ephemeral repo: %w", err)
		}
		r.logger.Info("registry: unregistered + purged ephemeral", "repo", row.ID, "path", row.LocalPath)
		return out, nil
	}
	if err := store.SetTracked(ctx, row.ID, false, false); err != nil {
		return out, fmt.Errorf("untrack: %w", err)
	}
	r.logger.Info("registry: unregistered (index kept)", "repo", row.ID, "path", row.LocalPath)
	return out, nil
}

// Delete stops watching the repo (if tracked) and removes it along with all
// its indexed data (CASCADE). Unlike Unregister, it always purges — durable
// or ephemeral.
func (r *Registrar) Delete(ctx context.Context, spec string) (Repo, error) {
	store := repos.New(r.db)
	row, err := r.resolve(ctx, store, spec)
	if err != nil {
		return Repo{}, err
	}
	out := Repo{ID: row.ID, Name: row.Name, Path: row.LocalPath, Ephemeral: row.Ephemeral}
	if r.tracker != nil {
		r.tracker.Untrack(row.ID)
	}
	if err := store.Delete(ctx, row.ID); err != nil {
		return out, fmt.Errorf("delete repo: %w", err)
	}
	r.logger.Info("registry: deleted repo + index", "repo", row.ID, "path", row.LocalPath)
	return out, nil
}

// Reindex triggers a non-destructive background re-index of the repo named by
// spec (id or local path). forceHealth forces a full biomarker recompute;
// forceWiki forces a full LLM wiki regeneration. Requires a live watcher.
func (r *Registrar) Reindex(ctx context.Context, spec, reason string, forceHealth, forceWiki bool) (Repo, error) {
	store := repos.New(r.db)
	row, err := r.resolve(ctx, store, spec)
	if err != nil {
		return Repo{}, err
	}
	out := Repo{ID: row.ID, Name: row.Name, Path: row.LocalPath, Ephemeral: row.Ephemeral}
	if r.tracker == nil {
		return out, ErrNoWatcher
	}
	if err := r.tracker.Reindex(row.ID, reason, forceHealth, forceWiki); err != nil {
		return out, err
	}
	r.logger.Info("registry: reindex triggered", "repo", row.ID, "reason", reason,
		"forceHealth", forceHealth, "forceWiki", forceWiki)
	return out, nil
}

// ListTracked returns the currently tracked repos.
func (r *Registrar) ListTracked(ctx context.Context) ([]Repo, error) {
	rows, err := repos.New(r.db).ListTracked(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Repo, 0, len(rows))
	for _, row := range rows {
		out = append(out, Repo{ID: row.ID, Name: row.Name, Path: row.LocalPath, Ephemeral: row.Ephemeral})
	}
	return out, nil
}

// resolve interprets spec as a repository id first, then a local path.
func (r *Registrar) resolve(ctx context.Context, store *repos.Store, spec string) (*repos.Repository, error) {
	if row, err := store.Get(ctx, spec); err == nil {
		return row, nil
	}
	if abs, err := absPath(spec); err == nil {
		if row, err := store.GetByLocalPath(ctx, abs); err == nil {
			return row, nil
		}
	}
	return nil, fmt.Errorf("no repository for %q (tried id and local path)", spec)
}

// absPath expands a leading ~ and resolves to an absolute path.
func absPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				p = home
			} else {
				p = filepath.Join(home, p[2:])
			}
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", p, err)
	}
	return abs, nil
}
