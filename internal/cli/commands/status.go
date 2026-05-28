package commands

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/persistence/externalstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/graphstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/healthstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/userconfig"
)

// newStatusCmd reads the persisted database for the configured repo and
// prints a one-screen summary. Doesn't re-ingest — purely a read view.
func newStatusCmd() *cobra.Command {
	var repoPath string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show a summary of the latest indexed state",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()

			repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, absRepoPath(repoPath), "")
			if err != nil {
				return fmt.Errorf("ensure repo: %w", err)
			}

			summary, err := collectStatus(ctx, conn, repoRow.ID)
			if err != nil {
				return err
			}

			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			defer tw.Flush()
			// Daemon record — printed first so the user sees the
			// big-picture "is anything running on this box" line
			// before drilling into one repo's numbers.
			if info, _ := userconfig.LoadDaemon(); info != nil {
				alive := info.Alive()
				state := "stale (process gone)"
				if alive {
					state = "running"
				}
				fmt.Fprintf(tw, "daemon\t%s\tpid=%d  addr=%s  since=%s\n",
					state, info.PID, info.Addr, info.StartedAt.Format("2006-01-02 15:04:05"))
				if info.WorkspaceRoot != "" {
					fmt.Fprintf(tw, "  workspace\t%s\n", info.WorkspaceRoot)
				}
				fmt.Fprintln(tw)
			}
			fmt.Fprintf(tw, "repository\t%s\n", repoRow.Name)
			fmt.Fprintf(tw, "local path\t%s\n", repoRow.LocalPath)
			if repoRow.HeadCommit != "" {
				fmt.Fprintf(tw, "head commit\t%s\n", repoRow.HeadCommit)
			}
			fmt.Fprintf(tw, "updated\t%s\n", repoRow.UpdatedAt.Format("2006-01-02 15:04:05"))
			fmt.Fprintln(tw)
			fmt.Fprintf(tw, "graph nodes\t%d\n", summary.GraphNodes)
			fmt.Fprintf(tw, "graph edges\t%d\n", summary.GraphEdges)
			fmt.Fprintf(tw, "git files\t%d\t(%d hotspots)\n", summary.GitFiles, summary.GitHotspots)
			fmt.Fprintf(tw, "externals\t%d\n", summary.ExternalsTotal)
			if len(summary.ExternalsByEco) > 0 {
				ecos := make([]string, 0, len(summary.ExternalsByEco))
				for e := range summary.ExternalsByEco {
					ecos = append(ecos, e)
				}
				sort.Strings(ecos)
				for _, e := range ecos {
					fmt.Fprintf(tw, "  %s\t%d\n", e, summary.ExternalsByEco[e])
				}
			}
			fmt.Fprintf(tw, "dead-code findings\t%d\n", summary.DeadCodeFindings)
			fmt.Fprintf(tw, "code health\tavg %.2f / 10\t(%d findings)\n", summary.AvgHealthScore, summary.HealthFindings)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	return cmd
}

type statusSummary struct {
	GraphNodes       int
	GraphEdges       int
	GitFiles         int
	GitHotspots      int
	ExternalsTotal   int
	ExternalsByEco   map[string]int
	DeadCodeFindings int
	HealthFindings   int
	AvgHealthScore   float64
}

func collectStatus(ctx context.Context, conn *sql.DB, repoID string) (statusSummary, error) {
	var s statusSummary

	gs := graphstore.New(conn)
	n, err := gs.CountNodes(ctx, repoID)
	if err != nil {
		return s, fmt.Errorf("count graph_nodes: %w", err)
	}
	s.GraphNodes = n
	edgeCounts, err := gs.CountByEdgeType(ctx, repoID)
	if err != nil {
		return s, fmt.Errorf("count graph_edges: %w", err)
	}
	for _, c := range edgeCounts {
		s.GraphEdges += c
	}

	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM git_metadata WHERE repository_id = ?`, repoID).Scan(&s.GitFiles); err != nil {
		return s, fmt.Errorf("count git_metadata: %w", err)
	}
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM git_metadata WHERE repository_id = ? AND is_hotspot = 1`, repoID).Scan(&s.GitHotspots); err != nil {
		return s, fmt.Errorf("count hotspots: %w", err)
	}

	es := externalstore.New(conn)
	s.ExternalsTotal, err = es.Count(ctx, repoID)
	if err != nil {
		return s, fmt.Errorf("count externals: %w", err)
	}
	s.ExternalsByEco, err = es.CountByEcosystem(ctx, repoID)
	if err != nil {
		return s, err
	}

	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dead_code_findings WHERE repository_id = ?`, repoID).Scan(&s.DeadCodeFindings); err != nil {
		return s, fmt.Errorf("count dead_code_findings: %w", err)
	}

	hs := healthstore.New(conn)
	s.HealthFindings, err = hs.CountFindings(ctx, repoID)
	if err != nil {
		return s, err
	}
	s.AvgHealthScore, err = hs.AverageScore(ctx, repoID)
	if err != nil {
		return s, err
	}
	return s, nil
}

// absRepoPath resolves repoPath to absolute, matching the path
// `ingest --persist` writes for the repository row.
func absRepoPath(repoPath string) string {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return repoPath
	}
	return abs
}
