// Workspace-aware bulk operations. Adds `workspace status` and the
// --workspace flag on existing single-repo commands (update, hook,
// doctor) so users with multiple repos under one workspace root can
// operate on the whole set with one invocation.
package commands

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/logging"
	"github.com/repowise-dev/repowise-go/internal/persistence/pipelinestore"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/pipeline"
	"github.com/repowise-dev/repowise-go/internal/workspace"
)

// newWorkspaceStatusCmd reads every member repo's latest pipeline run
// + headline counts and prints a single compact table — the "is my
// workspace healthy?" answer in one place.
func newWorkspaceStatusCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Cross-repo health + indexing status for the workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
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
				return nil
			}

			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  alias\tindexed?\tlast run\thealth\tfiles\tdecisions\tpages")
			for _, e := range s.Sorted() {
				row := collectRepoStatusRow(ctx, e)
				fmt.Fprintln(tw, row)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "workspace root")
	return cmd
}

// collectRepoStatusRow opens a per-repo DB connection and produces one
// tab-separated row. Failures degrade gracefully — the row shows
// "error" rather than aborting the whole listing.
func collectRepoStatusRow(ctx context.Context, e workspace.Entry) string {
	conn, _, err := openDB(ctx, e.Path)
	if err != nil {
		return fmt.Sprintf("  %s\t—\t—\t—\t—\t—\t— (err: %v)", e.Alias, err)
	}
	defer conn.Close()

	repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, e.Path, "")
	if err != nil {
		return fmt.Sprintf("  %s\t—\t—\t—\t—\t—\t— (no repo row)", e.Alias)
	}

	indexed := "no"
	latestRun := "—"
	latest, _ := pipelinestore.New(conn).LatestRun(ctx, repoRow.ID)
	if latest != nil {
		indexed = "yes"
		latestRun = fmt.Sprintf("%s (%s)", latest.Overall, latest.UpdatedAt.Format("2006-01-02 15:04"))
	}

	files, decisions, pages := 0, 0, 0
	var healthAvg sql.NullFloat64
	_ = conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_nodes WHERE repository_id = ? AND node_type = 'file'`,
		repoRow.ID).Scan(&files)
	_ = conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM decision_records WHERE repository_id = ?`, repoRow.ID).Scan(&decisions)
	_ = conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wiki_pages WHERE repository_id = ?`, repoRow.ID).Scan(&pages)
	_ = conn.QueryRowContext(ctx,
		`SELECT AVG(score) FROM health_file_metrics WHERE repository_id = ?`, repoRow.ID).Scan(&healthAvg)

	healthStr := "—"
	if healthAvg.Valid {
		healthStr = fmt.Sprintf("%.1f/10", healthAvg.Float64)
	}
	return fmt.Sprintf("  %s\t%s\t%s\t%s\t%d\t%d\t%d",
		e.Alias, indexed, latestRun, healthStr, files, decisions, pages)
}

// newWorkspaceUpdateCmd runs `repowise update` on every member repo.
// Mirrors the Python `repowise update --workspace` flow. Failures on
// one repo don't abort the others — each repo's success/error is
// reported, and the command returns a non-zero error only when at
// least one update failed.
func newWorkspaceUpdateCmd() *cobra.Command {
	var (
		root        string
		gitMaxCmts  int
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Re-index every repo in the workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ws, err := resolveWorkspaceRoot(root)
			if err != nil {
				return err
			}
			s, err := workspace.Load(ws)
			if err != nil {
				return err
			}
			if len(s.Repos) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no repos registered")
				return nil
			}
			logger := logging.New(logging.Options{
				Format: logging.FormatAuto, Level: logging.ParseLevel("info"), Out: os.Stderr,
			})
			out := cmd.OutOrStdout()
			failed := 0
			for _, e := range s.Sorted() {
				if err := updateOneRepo(ctx, e, gitMaxCmts, logger, out); err != nil {
					fmt.Fprintf(out, "  %s: FAILED — %v\n", e.Alias, err)
					failed++
					continue
				}
				fmt.Fprintf(out, "  %s: ok\n", e.Alias)
			}
			fmt.Fprintf(out, "\n%d updated, %d failed\n", len(s.Repos)-failed, failed)
			if failed > 0 {
				return fmt.Errorf("%d repo(s) failed to update", failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "workspace root")
	cmd.Flags().IntVar(&gitMaxCmts, "git-max-commits", 0, "cap commits walked by the git phase")
	_ = io.Discard // keep import alive (used by other helpers below)
	return cmd
}

func updateOneRepo(ctx context.Context, e workspace.Entry, gitMaxCmts int, logger *slog.Logger, out io.Writer) error {
	conn, _, err := openDB(ctx, e.Path)
	if err != nil {
		return err
	}
	defer conn.Close()
	repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, e.Path, "")
	if err != nil {
		return err
	}
	_, err = pipeline.Run(ctx, pipeline.Options{
		RepoPath:      e.Path,
		RepositoryID:  repoRow.ID,
		DB:            conn,
		Mode:          pipeline.ModeUpdate,
		GitMaxCommits: gitMaxCmts,
		Logger:        logger,
	})
	return err
}

// newWorkspaceHookCmd wires `workspace hook install/uninstall/status`
// — bulk over the existing single-repo hook logic. Reuses the helpers
// in hook.go.
func newWorkspaceHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Install / uninstall / report git hooks across the workspace",
	}
	cmd.AddCommand(workspaceHookSub("install", "Install the post-commit hook in every member repo",
		func(repoPath string) (string, error) {
			root, err := findGitRoot(repoPath)
			if err != nil {
				return "", err
			}
			return installHook(root)
		}))
	cmd.AddCommand(workspaceHookSub("uninstall", "Remove the post-commit hook from every member repo",
		func(repoPath string) (string, error) {
			root, err := findGitRoot(repoPath)
			if err != nil {
				return "", err
			}
			return uninstallHook(root)
		}))
	cmd.AddCommand(workspaceHookSub("status", "Report hook status for every member repo",
		func(repoPath string) (string, error) {
			root, err := findGitRoot(repoPath)
			if err != nil {
				return "", err
			}
			return hookStatus(root), nil
		}))
	return cmd
}

// workspaceHookSub builds one subcommand whose body iterates the
// workspace and applies fn to each repo's git root.
func workspaceHookSub(name, short string, fn func(string) (string, error)) *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   name,
		Short: short,
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
				fmt.Fprintln(out, "no repos registered")
				return nil
			}
			for _, e := range s.Sorted() {
				msg, err := fn(e.Path)
				if err != nil {
					fmt.Fprintf(out, "  %s: error — %v\n", e.Alias, err)
					continue
				}
				fmt.Fprintf(out, "  %s: %s\n", e.Alias, msg)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "workspace root")
	return cmd
}

// newWorkspaceDoctorCmd runs the doctor checks against every member
// repo, surfacing one summary line per repo. Doesn't replicate the
// full doctor table — that's per-repo invocation territory; this is
// triage: "which repos look broken?"
func newWorkspaceDoctorCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Quick health check across every workspace repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
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
				fmt.Fprintln(out, "no repos registered")
				return nil
			}
			for _, e := range s.Sorted() {
				issues := workspaceDoctorChecks(ctx, e)
				if len(issues) == 0 {
					fmt.Fprintf(out, "  %s: OK\n", e.Alias)
					continue
				}
				fmt.Fprintf(out, "  %s:\n", e.Alias)
				for _, issue := range issues {
					fmt.Fprintf(out, "    - %s\n", issue)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "workspace root")
	return cmd
}

// workspaceDoctorChecks runs a tiny battery of cheap checks per repo
// and returns a list of human-readable issues. Empty list = healthy.
func workspaceDoctorChecks(ctx context.Context, e workspace.Entry) []string {
	issues := []string{}
	if _, err := os.Stat(e.Path); err != nil {
		return []string{"path does not exist on disk: " + e.Path}
	}
	conn, _, err := openDB(ctx, e.Path)
	if err != nil {
		return []string{"cannot open repo DB: " + err.Error()}
	}
	defer conn.Close()
	repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, e.Path, "")
	if err != nil {
		return []string{"cannot resolve repo row: " + err.Error()}
	}
	latest, _ := pipelinestore.New(conn).LatestRun(ctx, repoRow.ID)
	if latest == nil {
		issues = append(issues, "never indexed (run `repowise update`)")
	} else if latest.Overall == pipelinestore.OutcomeFailed {
		issues = append(issues, fmt.Sprintf("latest pipeline run failed (%s)",
			latest.UpdatedAt.Format("2006-01-02")))
	}
	// Cheap "git root present" check.
	if _, err := findGitRoot(e.Path); err != nil {
		issues = append(issues, "no .git found at or above repo path")
	}
	return issues
}

// silence unused-import lint when only the bulk-update path runs.
var _ = errors.New

// gitRootHint exposes findGitRoot for use elsewhere if needed; the
// hook subcommands call findGitRoot directly via closure.
var _ = filepath.Join

// stringsTrimRightShim avoids re-importing strings in this file just
// for one call — silenced when unused.
var _ = strings.TrimRight
