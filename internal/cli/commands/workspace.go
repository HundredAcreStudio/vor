package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/workspace"
)

// newWorkspaceCmd is the multi-repo subcommand group. Workspace state
// lives in <root>/.repowise/workspace.json — registry of (alias, path)
// pairs plus a default alias. This is a lighter implementation than
// the Python `workspaces` system: no cross-repo contracts, no shared
// DB, no auto-sync hooks. Enough to land the CLI surface the README
// documents and give Phase 10 something to build on.
func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage a multi-repo workspace (list / add / remove / scan / set-default)",
	}
	cmd.AddCommand(newWorkspaceListCmd())
	cmd.AddCommand(newWorkspaceAddCmd())
	cmd.AddCommand(newWorkspaceRemoveCmd())
	cmd.AddCommand(newWorkspaceScanCmd())
	cmd.AddCommand(newWorkspaceSetDefaultCmd())
	return cmd
}

// resolveWorkspaceRoot picks the workspace root. We use the cwd by
// default, but defer to --root when supplied. The workspace.json
// file is stored at <root>/.repowise/workspace.json.
func resolveWorkspaceRoot(flag string) (string, error) {
	if flag == "" {
		flag = "."
	}
	abs, err := filepath.Abs(flag)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func newWorkspaceListCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List repos registered in this workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkspaceRoot(root)
			if err != nil {
				return err
			}
			s, err := workspace.Load(ws)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(s.Repos) == 0 {
				fmt.Fprintf(out, "no repos registered in %s\n", ws)
				fmt.Fprintln(out, "add one with `repowise workspace add <PATH>`")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  alias\tdefault\tpath")
			for _, e := range s.Sorted() {
				marker := " "
				if e.Alias == s.DefaultAlias {
					marker = "*"
				}
				fmt.Fprintf(tw, "  %s\t%s\t%s\n", e.Alias, marker, e.Path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "workspace root (where .repowise/workspace.json lives)")
	return cmd
}

func newWorkspaceAddCmd() *cobra.Command {
	var (
		root  string
		alias string
	)
	cmd := &cobra.Command{
		Use:   "add PATH",
		Short: "Register a repo in the workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkspaceRoot(root)
			if err != nil {
				return err
			}
			repoPath, err := filepath.Abs(args[0])
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}
			info, err := os.Stat(repoPath)
			if err != nil {
				return fmt.Errorf("path: %w", err)
			}
			if !info.IsDir() {
				return fmt.Errorf("%s is not a directory", repoPath)
			}

			s, err := workspace.Load(ws)
			if err != nil {
				return err
			}
			a := alias
			if a == "" {
				a = workspace.DefaultAliasFromPath(repoPath)
			}
			if err := s.Add(a, repoPath); err != nil {
				return err
			}
			if err := s.Save(ws); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added %s → %s (alias %q)\n", a, repoPath, a)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "workspace root")
	cmd.Flags().StringVar(&alias, "alias", "", "alias to register (default: derived from directory name)")
	return cmd
}

func newWorkspaceRemoveCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "remove ALIAS",
		Short: "Unregister a repo from the workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkspaceRoot(root)
			if err != nil {
				return err
			}
			s, err := workspace.Load(ws)
			if err != nil {
				return err
			}
			if err := s.Remove(args[0]); err != nil {
				return err
			}
			if err := s.Save(ws); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "workspace root")
	return cmd
}

func newWorkspaceSetDefaultCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "set-default ALIAS",
		Short: "Choose the default workspace repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkspaceRoot(root)
			if err != nil {
				return err
			}
			s, err := workspace.Load(ws)
			if err != nil {
				return err
			}
			if err := s.SetDefault(args[0]); err != nil {
				return err
			}
			if err := s.Save(ws); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "default → %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "workspace root")
	return cmd
}

func newWorkspaceScanCmd() *cobra.Command {
	var (
		root     string
		autoAdd  bool
		maxDepth int
	)
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Discover sibling git repos and (with --yes) auto-add them",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := resolveWorkspaceRoot(root)
			if err != nil {
				return err
			}
			found, err := scanForGitRepos(ws, maxDepth)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(found) == 0 {
				fmt.Fprintf(out, "no git repos found under %s (depth ≤ %d)\n", ws, maxDepth)
				return nil
			}

			s, err := workspace.Load(ws)
			if err != nil {
				return err
			}
			registered := map[string]bool{}
			for _, e := range s.Repos {
				registered[e.Path] = true
			}

			fmt.Fprintf(out, "found %d git repos:\n", len(found))
			added := 0
			for _, p := range found {
				status := " "
				if registered[p] {
					status = "(already registered)"
				}
				fmt.Fprintf(out, "  %s  %s\n", p, status)
				if registered[p] || !autoAdd {
					continue
				}
				alias := workspace.DefaultAliasFromPath(p)
				if err := s.Add(alias, p); err == nil {
					added++
				}
			}
			if autoAdd && added > 0 {
				if err := s.Save(ws); err != nil {
					return err
				}
				fmt.Fprintf(out, "\nadded %d new repos\n", added)
			} else if !autoAdd {
				fmt.Fprintln(out, "\nre-run with --yes to register the listed repos")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "workspace root")
	cmd.Flags().BoolVar(&autoAdd, "yes", false, "auto-add discovered repos without prompting")
	cmd.Flags().IntVar(&maxDepth, "depth", 3, "maximum directory depth to walk from the workspace root")
	return cmd
}

// scanForGitRepos returns the sorted set of git repositories found
// under root at depth ≤ maxDepth. A "git repository" is any directory
// containing a .git entry.
func scanForGitRepos(root string, maxDepth int) ([]string, error) {
	out := map[string]bool{}
	rootDepth := strings.Count(root, string(os.PathSeparator))
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		depth := strings.Count(path, string(os.PathSeparator)) - rootDepth
		if depth > maxDepth {
			return filepath.SkipDir
		}
		if filepath.Base(path) == ".git" {
			return filepath.SkipDir
		}
		// Cheap heuristic for "this directory is a git repo".
		if info, err := os.Stat(filepath.Join(path, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			out[path] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(out))
	for p := range out {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}

// silence unused-import lint if path errors aren't used.
var _ = errors.New
