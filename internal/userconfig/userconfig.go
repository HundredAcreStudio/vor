// Package userconfig manages the user-global state directory at
// $XDG_CONFIG_HOME/repowise (config) and $XDG_STATE_HOME/repowise
// (runtime state). On macOS / BSD without XDG vars set, this resolves
// to ~/.config/repowise and ~/.local/state/repowise respectively —
// the same locations the dot-config conventions expect.
//
// Layout:
//
//   $XDG_CONFIG_HOME/repowise/
//     config.yaml          user-global defaults (provider, model,
//                          db_url, log_level, ...) that override
//                          built-in defaults but lose to repo-local
//                          config and env vars.
//
//   $XDG_STATE_HOME/repowise/
//     daemon.json          last-started daemon (pid, addr, started_at,
//                          workspace_root). Written by `repowise
//                          serve` on startup, cleared on graceful
//                          shutdown.
//     workspaces.yaml      registry of known workspace roots so the
//                          daemon / status command don't need --root
//                          on every invocation.
//     watched.json         per-repo last-watched / last-update
//                          timestamps maintained by `repowise watch`.
package userconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ConfigDir returns the absolute path to the user-global config dir,
// creating it if it doesn't exist yet. Respects $XDG_CONFIG_HOME on
// Linux/BSD, falls back to os.UserConfigDir() everywhere else (which
// gives ~/Library/Application Support on macOS — but we deliberately
// prefer XDG's ~/.config layout since that's where repowise users
// have been keeping the per-repo config file).
func ConfigDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return ensureDir(filepath.Join(v, "repowise"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	// We override macOS default — see the package comment.
	return ensureDir(filepath.Join(home, ".config", "repowise"))
}

// StateDir is the runtime-state companion to ConfigDir. Honours
// $XDG_STATE_HOME; otherwise ~/.local/state/repowise on POSIX or
// %LOCALAPPDATA%\repowise on Windows.
func StateDir() (string, error) {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return ensureDir(filepath.Join(v, "repowise"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return ensureDir(filepath.Join(v, "repowise"))
		}
	}
	return ensureDir(filepath.Join(home, ".local", "state", "repowise"))
}

// ConfigPath returns the canonical path to config.yaml. The file may
// not exist yet — callers that read it should treat ENOENT as "use
// defaults".
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// DaemonPath returns the canonical path to daemon.json.
func DaemonPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.json"), nil
}

// WorkspacesPath returns the canonical path to workspaces.yaml.
func WorkspacesPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workspaces.yaml"), nil
}

// WatchedPath returns the canonical path to watched.json.
func WatchedPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "watched.json"), nil
}

func ensureDir(p string) (string, error) {
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", p, err)
	}
	return p, nil
}

// IsMissing returns true when err is a benign "file not found" — used
// by callers that treat missing config files as "fall back to defaults".
func IsMissing(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
