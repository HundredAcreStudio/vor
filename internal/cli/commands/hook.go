// Hook management — install / uninstall / status of the post-commit
// hook that runs `repowise update` after every commit. The hook script
// content matches the Python implementation byte-for-byte (modulo the
// "Installed by" attribution line) so users can switch between
// implementations on the same repo without re-installing.
package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	hookMarkerStart = "# repowise-hook-start"
	hookMarkerEnd   = "# repowise-hook-end"
)

// hookScript is the shell body installed between the markers. Kept in
// sync with packages/cli/src/repowise/cli/hooks.py:_HOOK_SCRIPT so the
// behaviour is identical across implementations:
//   - capture commit info into .repowise/.update.queued so augment can
//     see the pending update synchronously
//   - background the actual `repowise update` so the commit doesn't
//     block on it
//   - capture all output to .repowise/.update.log for diagnosis
const hookScript = `# repowise-hook-start
# Auto-syncs repowise wiki after each commit (background, non-blocking).
# Installed by: repowise hook install
{
  ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
  [ -d "$ROOT/.repowise" ] || exit 0
  HEAD=$(git rev-parse HEAD 2>/dev/null) || HEAD=""
  TS=$(date +%s 2>/dev/null) || TS=""
  if [ -n "$TS" ]; then
    printf '{"target_commit":"%s","queued_at":%s}\n' "$HEAD" "$TS" \
      > "$ROOT/.repowise/.update.queued" 2>/dev/null || true
  fi
  LOG="$ROOT/.repowise/.update.log"
  {
    printf '\n--- post-commit hook fired at %s for HEAD %s ---\n' \
      "$(date 2>/dev/null)" "$HEAD"
  } >> "$LOG" 2>/dev/null || true
  (
    cd "$ROOT" || exit 1
    if command -v repowise >/dev/null 2>&1; then
      repowise update >> "$LOG" 2>&1
    fi
  ) &
} >/dev/null 2>&1
# repowise-hook-end
`

// newHookCmd is the hook subcommand group.
func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Manage the git post-commit hook that runs `repowise update`",
	}
	cmd.AddCommand(newHookInstallCmd())
	cmd.AddCommand(newHookUninstallCmd())
	cmd.AddCommand(newHookStatusCmd())
	return cmd
}

func newHookInstallCmd() *cobra.Command {
	var repoPath string
	cmd := &cobra.Command{
		Use:   "install [PATH]",
		Short: "Write or update .git/hooks/post-commit",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveHookRoot(args, repoPath)
			if err != nil {
				return err
			}
			hookPath, err := installHook(root)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "hook installed at %s\n", hookPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (overridden by positional PATH)")
	return cmd
}

func newHookUninstallCmd() *cobra.Command {
	var repoPath string
	cmd := &cobra.Command{
		Use:   "uninstall [PATH]",
		Short: "Remove the repowise section from .git/hooks/post-commit",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveHookRoot(args, repoPath)
			if err != nil {
				return err
			}
			msg, err := uninstallHook(root)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (overridden by positional PATH)")
	return cmd
}

func newHookStatusCmd() *cobra.Command {
	var repoPath string
	cmd := &cobra.Command{
		Use:   "status [PATH]",
		Short: "Report whether the post-commit hook is installed",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveHookRoot(args, repoPath)
			if err != nil {
				return err
			}
			status := hookStatus(root)
			fmt.Fprintln(cmd.OutOrStdout(), status)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (overridden by positional PATH)")
	return cmd
}

// ---- core implementation -------------------------------------------------

// resolveHookRoot picks the path (positional > --repo) and walks up to
// the nearest .git directory.
func resolveHookRoot(args []string, repoFlag string) (string, error) {
	target := repoFlag
	if len(args) > 0 {
		target = args[0]
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	root, err := findGitRoot(abs)
	if err != nil {
		return "", err
	}
	return root, nil
}

// findGitRoot walks up from start until it finds a .git directory. Mirrors
// the Python helper.
func findGitRoot(start string) (string, error) {
	cur := start
	for {
		if info, err := os.Stat(filepath.Join(cur, ".git")); err == nil && info.IsDir() {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("no .git directory found at or above %s", start)
		}
		cur = parent
	}
}

// installHook writes a fresh hook or merges the marker block into an
// existing one. Returns the absolute hook file path.
func installHook(root string) (string, error) {
	hooksDir := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", err
	}
	hookPath := filepath.Join(hooksDir, "post-commit")

	existing, err := os.ReadFile(hookPath)
	if errors.Is(err, os.ErrNotExist) {
		content := "#!/bin/sh\n\n" + hookScript
		if err := os.WriteFile(hookPath, []byte(content), 0o755); err != nil {
			return "", err
		}
		return hookPath, nil
	}
	if err != nil {
		return "", err
	}
	updated := mergeHookBlock(string(existing), hookScript)
	if err := os.WriteFile(hookPath, []byte(updated), 0o755); err != nil {
		return "", err
	}
	return hookPath, nil
}

// uninstallHook removes the marker block from the hook file. Returns a
// user-facing status message.
func uninstallHook(root string) (string, error) {
	hookPath := filepath.Join(root, ".git", "hooks", "post-commit")
	existing, err := os.ReadFile(hookPath)
	if errors.Is(err, os.ErrNotExist) {
		return "no post-commit hook found", nil
	}
	if err != nil {
		return "", err
	}
	stripped, removed := stripHookBlock(string(existing))
	if !removed {
		return "repowise hook not found in post-commit", nil
	}
	if strings.TrimSpace(stripped) == "" || strings.TrimSpace(stripped) == "#!/bin/sh" {
		// Don't leave a useless empty hook around.
		if err := os.Remove(hookPath); err != nil {
			return "", err
		}
		return "removed repowise hook (post-commit was otherwise empty; deleted)", nil
	}
	if err := os.WriteFile(hookPath, []byte(stripped), 0o755); err != nil {
		return "", err
	}
	return "removed repowise hook section from post-commit", nil
}

// hookStatus returns a one-line summary of the hook's state.
func hookStatus(root string) string {
	hookPath := filepath.Join(root, ".git", "hooks", "post-commit")
	body, err := os.ReadFile(hookPath)
	if errors.Is(err, os.ErrNotExist) {
		return "no post-commit hook installed"
	}
	if err != nil {
		return "error reading hook: " + err.Error()
	}
	if strings.Contains(string(body), hookMarkerStart) && strings.Contains(string(body), hookMarkerEnd) {
		return "repowise hook installed at " + hookPath
	}
	return "post-commit hook exists but contains no repowise section: " + hookPath
}

// mergeHookBlock inserts or replaces the repowise block inside existing
// content. Preserves everything outside the marker pair.
func mergeHookBlock(existing, block string) string {
	startIdx := strings.Index(existing, hookMarkerStart)
	endIdx := strings.Index(existing, hookMarkerEnd)
	if startIdx >= 0 && endIdx > startIdx {
		head := strings.TrimRight(existing[:startIdx], "\n")
		tail := strings.TrimLeft(existing[endIdx+len(hookMarkerEnd):], "\n")
		out := head
		if out != "" {
			out += "\n\n"
		}
		out += block
		if tail != "" {
			out += "\n" + tail
		}
		return out
	}
	trimmed := strings.TrimRight(existing, "\n")
	if !strings.HasPrefix(trimmed, "#!") {
		trimmed = "#!/bin/sh\n\n" + trimmed
	}
	return trimmed + "\n\n" + block
}

// stripHookBlock removes the marker pair (and the content between them)
// from content. Returns the cleaned content and whether anything was
// removed.
func stripHookBlock(content string) (string, bool) {
	startIdx := strings.Index(content, hookMarkerStart)
	endIdx := strings.Index(content, hookMarkerEnd)
	if startIdx < 0 || endIdx < 0 || endIdx <= startIdx {
		return content, false
	}
	head := strings.TrimRight(content[:startIdx], "\n")
	tail := strings.TrimLeft(content[endIdx+len(hookMarkerEnd):], "\n")
	cleaned := head
	if tail != "" {
		if cleaned != "" {
			cleaned += "\n\n"
		}
		cleaned += tail
	}
	if cleaned != "" && !strings.HasSuffix(cleaned, "\n") {
		cleaned += "\n"
	}
	return cleaned, true
}
