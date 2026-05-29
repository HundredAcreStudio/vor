package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// The vor-augment PostToolUse hook enriches an agent's Grep/Glob/Bash output
// with indexed context. `vor hook install` wires it into a Claude Code
// settings.json; `vor hook uninstall` removes it. Both are idempotent.
const (
	augmentBinary  = "vor-augment"
	augmentMatcher = "Bash|Grep|Glob"
	augmentTimeout = 10
)

// newHookCmd is the parent for the hook install/uninstall subcommands.
func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Install/remove the vor-augment Claude Code hook",
		Long: `Manage the vor-augment PostToolUse hook in a Claude Code settings.json.

The hook runs after every Grep/Glob/Bash and enriches the agent's output with
context from the vor index — the closest indexed symbol for a search that
missed, the most central files for a search that flooded, or a stale-index
notice after a commit. It stays silent otherwise and never blocks a tool call.`,
	}
	cmd.AddCommand(newHookInstallCmd())
	cmd.AddCommand(newHookUninstallCmd())
	return cmd
}

func newHookInstallCmd() *cobra.Command {
	var global bool
	var project bool
	var force bool

	cmd := &cobra.Command{
		Use:   "install [project-dir]",
		Short: "Add the vor-augment hook to a Claude Code settings.json",
		Long: `Merge the vor-augment PostToolUse hook into a settings.json.

By default it edits the global settings (~/.claude/settings.json), so the hook
is active in every session — it makes itself a no-op outside repos that vor has
indexed. Pass --project to edit ./.claude/settings.json instead (or give a
directory), which checks the hook into a single repo for everyone who clones it.

Idempotent: re-running leaves an existing entry untouched unless --force.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, scope, err := resolveSettingsPath(global, project, args)
			if err != nil {
				return err
			}
			command := resolveAugmentCommand(cmd.ErrOrStderr())

			changed, err := applyHookInstall(path, command, force)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if changed {
				fmt.Fprintf(out, "Installed vor-augment hook (%s) in %s settings: %s\n", command, scope, path)
				fmt.Fprintln(out, "Restart Claude Code (or start a new session) to pick it up.")
			} else {
				fmt.Fprintf(out, "vor-augment hook already present in %s settings: %s\n", scope, path)
				fmt.Fprintln(out, "Pass --force to rewrite the entry.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "edit ~/.claude/settings.json (the default)")
	cmd.Flags().BoolVar(&project, "project", false, "edit ./.claude/settings.json (checked into the repo)")
	cmd.Flags().BoolVar(&force, "force", false, "rewrite the hook entry even if one already exists")
	return cmd
}

func newHookUninstallCmd() *cobra.Command {
	var global bool
	var project bool

	cmd := &cobra.Command{
		Use:   "uninstall [project-dir]",
		Short: "Remove the vor-augment hook from a Claude Code settings.json",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, scope, err := resolveSettingsPath(global, project, args)
			if err != nil {
				return err
			}
			changed, err := applyHookUninstall(path)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if changed {
				fmt.Fprintf(out, "Removed vor-augment hook from %s settings: %s\n", scope, path)
			} else {
				fmt.Fprintf(out, "No vor-augment hook found in %s settings: %s\n", scope, path)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "edit ~/.claude/settings.json (the default)")
	cmd.Flags().BoolVar(&project, "project", false, "edit ./.claude/settings.json (checked into the repo)")
	return cmd
}

// resolveSettingsPath picks the settings.json to edit. Global is the default;
// --project (or a directory arg) targets a repo-local .claude/settings.json.
func resolveSettingsPath(global, project bool, args []string) (path, scope string, err error) {
	if global && (project || len(args) == 1) {
		return "", "", fmt.Errorf("--global cannot be combined with --project or a project directory")
	}
	if project || len(args) == 1 {
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", "", fmt.Errorf("resolve project dir %q: %w", dir, err)
		}
		return filepath.Join(abs, ".claude", "settings.json"), "project", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), "global", nil
}

// resolveAugmentCommand returns the absolute path to vor-augment when it is on
// PATH (more robust than a bare name in the hook's exec environment), falling
// back to the bare name with a warning so install still succeeds.
func resolveAugmentCommand(warn interface{ Write([]byte) (int, error) }) string {
	if p, err := exec.LookPath(augmentBinary); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	fmt.Fprintf(warn, "warning: %q is not on PATH; run `make install` (or `go install ./cmd/vor-augment`) so the hook can run.\n", augmentBinary)
	return augmentBinary
}

// applyHookInstall reads settingsPath, merges in the vor-augment hook, and
// writes it back. Returns whether the file changed.
func applyHookInstall(settingsPath, command string, force bool) (bool, error) {
	settings, err := readSettings(settingsPath)
	if err != nil {
		return false, err
	}
	if !addAugmentHook(settings, command, force) {
		return false, nil
	}
	if err := writeSettings(settingsPath, settings); err != nil {
		return false, err
	}
	return true, nil
}

// applyHookUninstall removes the vor-augment hook from settingsPath. Returns
// whether the file changed. A missing file is treated as "nothing to remove".
func applyHookUninstall(settingsPath string) (bool, error) {
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		return false, nil
	}
	settings, err := readSettings(settingsPath)
	if err != nil {
		return false, err
	}
	if !removeAugmentHook(settings) {
		return false, nil
	}
	if err := writeSettings(settingsPath, settings); err != nil {
		return false, err
	}
	return true, nil
}

// readSettings loads a settings.json into a generic map, preserving any keys
// vor doesn't manage. A missing file yields an empty map.
func readSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse %s (is it valid JSON?): %w", path, err)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, nil
}

// writeSettings writes settings back as indented JSON, creating parent dirs.
func writeSettings(path string, settings map[string]any) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	data = append(data, '\n')
	return writeFile(path, data)
}

// postToolUseGroups returns the PostToolUse hook groups as a []any, or nil if
// the structure is absent or not the expected shape.
func postToolUseGroups(settings map[string]any) []any {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	groups, _ := hooks["PostToolUse"].([]any)
	return groups
}

// groupHasAugment reports whether a PostToolUse group runs the augment binary.
func groupHasAugment(group any) bool {
	g, ok := group.(map[string]any)
	if !ok {
		return false
	}
	inner, ok := g["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range inner {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, _ := hm["command"].(string); commandIsAugment(cmd) {
			return true
		}
	}
	return false
}

// commandIsAugment matches both a bare `vor-augment` and an absolute path to it.
func commandIsAugment(cmd string) bool {
	return cmd == augmentBinary || filepath.Base(cmd) == augmentBinary
}

// addAugmentHook inserts the canonical vor-augment group into settings'
// PostToolUse hooks. It is idempotent: an existing group is left alone unless
// force is set, in which case it is replaced. Returns whether settings changed.
func addAugmentHook(settings map[string]any, command string, force bool) bool {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	groups, _ := hooks["PostToolUse"].([]any)

	if hasAugmentGroup(groups) {
		if !force {
			return false
		}
		groups = stripAugmentGroups(groups)
	}

	groups = append(groups, augmentGroup(command))
	hooks["PostToolUse"] = groups
	return true
}

// removeAugmentHook deletes every vor-augment group from settings, pruning the
// emptied PostToolUse array and hooks object so we don't leave dead scaffolding.
// Returns whether settings changed.
func removeAugmentHook(settings map[string]any) bool {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return false
	}
	groups, ok := hooks["PostToolUse"].([]any)
	if !ok || !hasAugmentGroup(groups) {
		return false
	}
	remaining := stripAugmentGroups(groups)
	if len(remaining) == 0 {
		delete(hooks, "PostToolUse")
	} else {
		hooks["PostToolUse"] = remaining
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	}
	return true
}

func hasAugmentGroup(groups []any) bool {
	for _, g := range groups {
		if groupHasAugment(g) {
			return true
		}
	}
	return false
}

func stripAugmentGroups(groups []any) []any {
	out := make([]any, 0, len(groups))
	for _, g := range groups {
		if !groupHasAugment(g) {
			out = append(out, g)
		}
	}
	return out
}

// augmentGroup builds the canonical PostToolUse matcher group for the hook.
func augmentGroup(command string) map[string]any {
	return map[string]any{
		"matcher": augmentMatcher,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": command,
				"timeout": augmentTimeout,
			},
		},
	}
}
