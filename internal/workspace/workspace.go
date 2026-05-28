// Package workspace manages a small JSON-backed registry of repos
// that should be treated as one logical multi-repo. Stored at
// <ws-root>/.repowise/workspace.json. Each entry pairs a repo path
// with an alias (free-form short name the user types) and tracks the
// default — the repo `repowise` operates on when called from the
// workspace root without specifying one.
//
// This is a lighter implementation than the Python `workspaces` system
// (no auto-sync hooks, no shared DB, no cross-repo contracts yet) —
// enough surface to land the CLI commands documented in the README:
// list / add / remove / scan / set-default.
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one repo registered in the workspace.
type Entry struct {
	Alias string `json:"alias"`
	Path  string `json:"path"` // absolute
}

// State is the persisted workspace.
type State struct {
	Repos        []Entry `json:"repos"`
	DefaultAlias string  `json:"default_alias,omitempty"`
}

// statePath returns <root>/.repowise/workspace.json.
func statePath(root string) string {
	return filepath.Join(root, ".repowise", "workspace.json")
}

// FindRoot walks up from start looking for a .repowise/workspace.json.
// Returns "" + nil error when none is found — the caller decides how
// to handle absence.
func FindRoot(start string) (string, error) {
	cur, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(cur, ".repowise", "workspace.json")); err == nil {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", nil
		}
		cur = parent
	}
}

// Load reads the workspace state from <root>/.repowise/workspace.json.
// Returns a zero State (no error) when no file exists yet.
func Load(root string) (*State, error) {
	body, err := os.ReadFile(statePath(root))
	if errors.Is(err, os.ErrNotExist) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("parse workspace.json: %w", err)
	}
	return &s, nil
}

// Save writes the workspace state back to disk, creating .repowise/
// if needed.
func (s *State) Save(root string) error {
	if err := os.MkdirAll(filepath.Join(root, ".repowise"), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(root), body, 0o644)
}

// Add registers a new entry. Returns an error if the alias is already
// taken or the path is already registered under a different alias.
// When the workspace is empty, the new entry becomes the default.
func (s *State) Add(alias, absPath string) error {
	if alias == "" {
		return errors.New("alias is required")
	}
	for _, e := range s.Repos {
		if e.Alias == alias {
			return fmt.Errorf("alias %q already exists (path %s)", alias, e.Path)
		}
		if e.Path == absPath {
			return fmt.Errorf("path %s already registered as %q", absPath, e.Alias)
		}
	}
	s.Repos = append(s.Repos, Entry{Alias: alias, Path: absPath})
	if s.DefaultAlias == "" {
		s.DefaultAlias = alias
	}
	return nil
}

// Remove unregisters an entry by alias. Returns an error if the alias
// doesn't exist. If the removed entry was the default, the default
// switches to the first remaining entry (or empties out).
func (s *State) Remove(alias string) error {
	idx := -1
	for i, e := range s.Repos {
		if e.Alias == alias {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("no repo registered with alias %q", alias)
	}
	s.Repos = append(s.Repos[:idx], s.Repos[idx+1:]...)
	if s.DefaultAlias == alias {
		s.DefaultAlias = ""
		if len(s.Repos) > 0 {
			s.DefaultAlias = s.Repos[0].Alias
		}
	}
	return nil
}

// SetDefault picks the default alias. Errors when the alias isn't
// registered.
func (s *State) SetDefault(alias string) error {
	for _, e := range s.Repos {
		if e.Alias == alias {
			s.DefaultAlias = alias
			return nil
		}
	}
	return fmt.Errorf("no repo registered with alias %q", alias)
}

// Lookup returns the entry for an alias, or false when missing.
func (s *State) Lookup(alias string) (Entry, bool) {
	for _, e := range s.Repos {
		if e.Alias == alias {
			return e, true
		}
	}
	return Entry{}, false
}

// Sorted returns a copy of Repos sorted by alias. Useful for
// deterministic CLI output.
func (s *State) Sorted() []Entry {
	out := make([]Entry, len(s.Repos))
	copy(out, s.Repos)
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
}

// DefaultAliasFromPath returns a reasonable alias from an absolute
// repo path — the directory's basename, lowercased, with non-alnum
// chars collapsed to '-'.
func DefaultAliasFromPath(absPath string) string {
	base := filepath.Base(absPath)
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(base) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}
