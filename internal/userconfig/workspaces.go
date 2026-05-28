package userconfig

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// WorkspaceRegistry is the user-global list of known workspace roots.
// Lives at $XDG_STATE_HOME/repowise/workspaces.yaml. Distinct from a
// workspace's own .repowise/workspace.json — that file lists the
// member repos of one workspace; this one lists the workspace roots
// themselves so the daemon / status command don't need --root.
type WorkspaceRegistry struct {
	Workspaces []WorkspaceEntry `yaml:"workspaces"`
}

// WorkspaceEntry records one root path. Label is optional; useful
// when several workspaces live under one machine (e.g. "work", "side").
type WorkspaceEntry struct {
	Path         string    `yaml:"path"`
	Label        string    `yaml:"label,omitempty"`
	RegisteredAt time.Time `yaml:"registered_at,omitempty"`
}

// LoadWorkspaces reads the registry. Returns an empty *WorkspaceRegistry
// (not nil) when the file is missing so callers can append without nil-
// checking.
func LoadWorkspaces() (*WorkspaceRegistry, error) {
	path, err := WorkspacesPath()
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &WorkspaceRegistry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var r WorkspaceRegistry
	if err := yaml.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse workspaces.yaml: %w", err)
	}
	return &r, nil
}

// SaveWorkspaces writes the registry back. Sorts by path so the file
// is diff-friendly across edits.
func SaveWorkspaces(r *WorkspaceRegistry) error {
	path, err := WorkspacesPath()
	if err != nil {
		return err
	}
	out := append([]WorkspaceEntry(nil), r.Workspaces...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	r.Workspaces = out
	body, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

// Add registers path with the given (optional) label. Returns an
// error if path is already registered.
func (r *WorkspaceRegistry) Add(path, label string) error {
	for _, e := range r.Workspaces {
		if e.Path == path {
			return fmt.Errorf("workspace %s already registered", path)
		}
	}
	r.Workspaces = append(r.Workspaces, WorkspaceEntry{
		Path: path, Label: label, RegisteredAt: time.Now().UTC(),
	})
	return nil
}

// Remove drops the entry for path. Returns an error if it isn't there.
func (r *WorkspaceRegistry) Remove(path string) error {
	for i, e := range r.Workspaces {
		if e.Path == path {
			r.Workspaces = append(r.Workspaces[:i], r.Workspaces[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("workspace %s not registered", path)
}
