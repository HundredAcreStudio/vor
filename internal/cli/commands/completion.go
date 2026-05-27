package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// newCompletionCmd replaces cobra's auto-generated completion command with one
// that can both print a shell completion script (the default) and, with
// --install, write it to a file and wire it into the user's shell rc.
func newCompletionCmd() *cobra.Command {
	var install bool
	var force bool

	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate (and optionally install) shell completion scripts",
		Long: `Generate a shell completion script for repowise.

By default the script is printed to stdout so you can source or redirect it
yourself, e.g.:

    source <(repowise completion zsh)

Pass --install to write the script to a file under ~/.config/repowise and add a
line to your shell rc (~/.bashrc, ~/.zshrc) that sources it. Fish scripts are
written to fish's autoloaded completions directory and need no rc change.

If no shell is given it is detected from $SHELL.`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			shell, err := resolveShell(args)
			if err != nil {
				return err
			}
			if install {
				return installCompletion(cmd, shell, force)
			}
			return writeCompletion(cmd.Root(), shell, cmd.OutOrStdout())
		},
	}

	cmd.Flags().BoolVar(&install, "install", false, "write the script to a file and source it from your shell rc")
	cmd.Flags().BoolVar(&force, "force", false, "with --install, rewrite the rc entry even if it already exists")
	return cmd
}

// resolveShell returns the shell named in args, or detects it from $SHELL.
func resolveShell(args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	shell := filepath.Base(os.Getenv("SHELL"))
	switch shell {
	case "bash", "zsh", "fish":
		return shell, nil
	default:
		return "", fmt.Errorf("could not detect shell from $SHELL (%q); pass one of bash|zsh|fish|powershell", shell)
	}
}

// writeCompletion emits the completion script for shell to w.
func writeCompletion(root *cobra.Command, shell string, w io.Writer) error {
	switch shell {
	case "bash":
		return root.GenBashCompletionV2(w, true)
	case "zsh":
		return root.GenZshCompletion(w)
	case "fish":
		return root.GenFishCompletion(w, true)
	case "powershell":
		return root.GenPowerShellCompletionWithDesc(w)
	default:
		return fmt.Errorf("unsupported shell %q (want bash|zsh|fish|powershell)", shell)
	}
}

// installCompletion writes the completion script to a file and, for bash/zsh,
// ensures the user's rc file sources it.
func installCompletion(cmd *cobra.Command, shell string, force bool) error {
	if shell == "powershell" {
		return fmt.Errorf("--install is not supported for powershell; add the output of `repowise completion powershell` to your $PROFILE")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}

	var buf bytes.Buffer
	if err := writeCompletion(cmd.Root(), shell, &buf); err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	// Fish autoloads from its completions dir, so no rc edit is needed.
	if shell == "fish" {
		dst := filepath.Join(home, ".config", "fish", "completions", "repowise.fish")
		if err := writeFile(dst, buf.Bytes()); err != nil {
			return err
		}
		fmt.Fprintf(out, "Installed fish completion to %s\nRestart fish or run: source %s\n", dst, dst)
		return nil
	}

	scriptPath := filepath.Join(home, ".config", "repowise", "completion."+shell)
	if err := writeFile(scriptPath, buf.Bytes()); err != nil {
		return err
	}

	rcPath := filepath.Join(home, "."+shell+"rc")
	added, err := ensureRCSource(rcPath, scriptPath, force)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Installed %s completion to %s\n", shell, scriptPath)
	if added {
		fmt.Fprintf(out, "Added source line to %s\nRestart your shell or run: source %s\n", rcPath, rcPath)
	} else {
		fmt.Fprintf(out, "%s already sources it; run `source %s` to reload\n", rcPath, rcPath)
	}
	return nil
}

const (
	rcMarkerStart = "# >>> repowise completion >>>"
	rcMarkerEnd   = "# <<< repowise completion <<<"
)

// ensureRCSource appends a guarded block to rcPath that sources scriptPath.
// It is idempotent: if the block already exists it is left alone unless force
// is set, in which case it is replaced. Returns whether the block was written.
func ensureRCSource(rcPath, scriptPath string, force bool) (bool, error) {
	existing, err := os.ReadFile(rcPath)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", rcPath, err)
	}

	block := fmt.Sprintf("%s\n[ -s %q ] && source %q\n%s\n", rcMarkerStart, scriptPath, scriptPath, rcMarkerEnd)
	content := string(existing)

	if strings.Contains(content, rcMarkerStart) {
		if !force {
			return false, nil
		}
		content = stripRCBlock(content)
	}

	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += block

	if err := writeFile(rcPath, []byte(content)); err != nil {
		return false, err
	}
	return true, nil
}

// stripRCBlock removes a previously written marker block from rc content.
func stripRCBlock(content string) string {
	start := strings.Index(content, rcMarkerStart)
	if start < 0 {
		return content
	}
	end := strings.Index(content, rcMarkerEnd)
	if end < 0 {
		return content[:start]
	}
	end += len(rcMarkerEnd)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	// Trim a trailing blank line left behind before the block.
	before := strings.TrimRight(content[:start], "\n")
	if before != "" {
		before += "\n"
	}
	return before + content[end:]
}

// writeFile creates parent dirs and writes data to path.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
