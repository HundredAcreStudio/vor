package userconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"
)

// WatchedRegistry tracks every repo `repowise watch` has been invoked
// against. Updated on each watch start + on every successful auto-
// update fired inside the watcher. Lives at
// $XDG_STATE_HOME/repowise/watched.json.
//
// Useful for telemetry ("which repos am I actually keeping fresh?")
// and for the upcoming `repowise status --watched` summary that
// reports the last-update time per repo without round-tripping every
// DB.
type WatchedRegistry struct {
	Repos []WatchedRepo `json:"repos"`
}

type WatchedRepo struct {
	Path           string    `json:"path"`
	Alias          string    `json:"alias,omitempty"`
	WorkspaceRoot  string    `json:"workspace_root,omitempty"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastWatchedAt  time.Time `json:"last_watched_at"`
	LastUpdatedAt  time.Time `json:"last_updated_at,omitempty"`
	UpdateCount    int       `json:"update_count"`
}

// LoadWatched reads the registry. Returns an empty *WatchedRegistry
// when the file's missing so callers can append + Save without nil
// checks.
func LoadWatched() (*WatchedRegistry, error) {
	path, err := WatchedPath()
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &WatchedRegistry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var r WatchedRegistry
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse watched.json: %w", err)
	}
	return &r, nil
}

// SaveWatched atomically writes the registry. Sorts by path for diff-
// friendliness.
func SaveWatched(r *WatchedRegistry) error {
	path, err := WatchedPath()
	if err != nil {
		return err
	}
	sort.Slice(r.Repos, func(i, j int) bool { return r.Repos[i].Path < r.Repos[j].Path })
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RecordWatch updates (or inserts) the entry for path. WorkspaceRoot
// and Alias are optional — they're recorded the first time we see the
// path and not overwritten afterwards (so workspace-aware tagging
// doesn't get lost across an explicit `repowise watch PATH` run).
func (r *WatchedRegistry) RecordWatch(path, alias, workspaceRoot string) {
	now := time.Now().UTC()
	for i := range r.Repos {
		if r.Repos[i].Path == path {
			r.Repos[i].LastWatchedAt = now
			if r.Repos[i].Alias == "" {
				r.Repos[i].Alias = alias
			}
			if r.Repos[i].WorkspaceRoot == "" {
				r.Repos[i].WorkspaceRoot = workspaceRoot
			}
			return
		}
	}
	r.Repos = append(r.Repos, WatchedRepo{
		Path:          path,
		Alias:         alias,
		WorkspaceRoot: workspaceRoot,
		FirstSeenAt:   now,
		LastWatchedAt: now,
	})
}

// RecordUpdate bumps LastUpdatedAt + UpdateCount for the entry. The
// repo must have been previously watched — silent no-op otherwise so
// callers don't have to coordinate with RecordWatch.
func (r *WatchedRegistry) RecordUpdate(path string) {
	now := time.Now().UTC()
	for i := range r.Repos {
		if r.Repos[i].Path == path {
			r.Repos[i].LastUpdatedAt = now
			r.Repos[i].UpdateCount++
			return
		}
	}
}
