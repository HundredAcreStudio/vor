// Workspace cross-repo co-change detection — the actual differentiator
// of workspace mode vs running update in a loop. Walks every member
// repo's git log, buckets commits by author + time window, surfaces
// pairs of files in different repos that change together.
package commands

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/workspace"
)

// newWorkspaceCoChangesCmd computes (or reads cached) cross-repo
// co-change pairs and prints them as a table. `--refresh` recomputes;
// otherwise we use the cached report at .repowise/co_changes.json
// when available.
func newWorkspaceCoChangesCmd() *cobra.Command {
	var (
		root     string
		refresh  bool
		window   int
		minCount int
		limit    int
		repoPair string
	)
	cmd := &cobra.Command{
		Use:     "co-changes",
		Aliases: []string{"cochanges"},
		Short:   "Detect file pairs across repos that change together (workspace-wide)",
		Long: `Walks every member repo's git log, groups commits by author
within a configurable time window, and surfaces pairs of files in
different repos that consistently co-change.

Reads a cached report from <root>/.repowise/co_changes.json when
present. Pass --refresh to recompute. The cache is written on
every refresh so subsequent invocations are instant.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ws, err := resolveWorkspaceRoot(root)
			if err != nil {
				return err
			}
			state, err := workspace.Load(ws)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(state.Repos) < 2 {
				fmt.Fprintln(out, "co-change detection needs ≥2 registered repos")
				return nil
			}

			report, err := loadOrCompute(ctx, ws, state.Repos, refresh, window, minCount)
			if err != nil {
				return err
			}
			if report == nil {
				fmt.Fprintln(out, "no co-changes found")
				return nil
			}
			renderCoChangeTable(out, report, limit, repoPair)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "workspace root")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "recompute (otherwise use cached report)")
	cmd.Flags().IntVar(&window, "window", 10, "commit-pairing time window in minutes")
	cmd.Flags().IntVar(&minCount, "min-count", 2, "minimum cross-repo co-change events to surface a pair")
	cmd.Flags().IntVar(&limit, "limit", 30, "max pairs to show")
	cmd.Flags().StringVar(&repoPair, "between", "",
		"only show pairs between two named repos: api,web")
	return cmd
}

// loadOrCompute returns the cached report when refresh=false and a
// cache exists; otherwise recomputes and saves.
func loadOrCompute(
	ctx context.Context, root string, members []workspace.Entry,
	refresh bool, window, minCount int,
) (*workspace.CoChangeReport, error) {
	if !refresh {
		if cached, err := workspace.LoadReport(root); err == nil && cached != nil {
			return cached, nil
		}
	}
	report, err := workspace.DetectCrossRepoCoChanges(ctx, members, workspace.DetectOptions{
		WindowMinutes: window,
		MinCount:      minCount,
	})
	if err != nil {
		return nil, err
	}
	if err := workspace.SaveReport(root, report); err != nil {
		// Persistence failure is non-fatal — still return the result.
		fmt.Fprintf(nopWriter{}, "save: %v", err)
	}
	return report, nil
}

// renderCoChangeTable prints the pair list with simple filtering.
func renderCoChangeTable(out io.Writer, r *workspace.CoChangeReport, limit int, repoPair string) {
	var betweenA, betweenB string
	if repoPair != "" {
		parts := strings.SplitN(repoPair, ",", 2)
		if len(parts) == 2 {
			betweenA = strings.TrimSpace(parts[0])
			betweenB = strings.TrimSpace(parts[1])
			if betweenA > betweenB {
				betweenA, betweenB = betweenB, betweenA
			}
		}
	}

	fmt.Fprintf(out, "Generated: %s  window=%dmin  min_count=%d  members=%s\n\n",
		r.GeneratedAt, r.Window, r.MinCount, strings.Join(r.Members, ","))

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "  count\trepo A\tfile A\trepo B\tfile B\tlast seen")
	shown := 0
	for _, p := range r.Pairs {
		if betweenA != "" {
			a, b := p.RepoA, p.RepoB
			if a > b {
				a, b = b, a
			}
			if a != betweenA || b != betweenB {
				continue
			}
		}
		if limit > 0 && shown >= limit {
			break
		}
		fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\t%s\t%s\n",
			p.Count, p.RepoA, p.FileA, p.RepoB, p.FileB, p.LastSeenAt)
		shown++
	}
	if shown == 0 {
		fmt.Fprintln(tw, "  (no pairs match the filter)")
	}
}

// nopWriter discards everything written to it. Used for non-fatal
// persistence-error messages we don't want to surface.
type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
