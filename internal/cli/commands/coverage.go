package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/analysis/health/coverage"
	"github.com/repowise-dev/repowise-go/internal/persistence/coveragestore"
)

// newCoverageCmd groups coverage import/status. Coverage feeds the
// untested_hotspot biomarker: once imported, a hotspot is judged untested
// by real line coverage rather than the paired-test-file heuristic.
func newCoverageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "coverage",
		Short: "Import and inspect test-coverage reports (LCOV / Cobertura)",
	}
	cmd.AddCommand(newCoverageImportCmd())
	cmd.AddCommand(newCoverageStatusCmd())
	return cmd
}

func newCoverageImportCmd() *cobra.Command {
	var (
		repoPath    string
		repoID      string
		format      string
		stripPrefix string
		commitSHA   string
	)
	cmd := &cobra.Command{
		Use:   "import <report-file>",
		Short: "Parse a coverage report and store per-file line coverage",
		Long: `Parses an LCOV (.info) or Cobertura XML report and stores per-file
line coverage. File paths are normalised to repo-relative POSIX form;
use --strip-prefix when the report paths carry an extra leading segment
(e.g. a CI workspace dir). Re-run after ` + "`repowise update`" + ` so the
untested_hotspot biomarker uses the fresh numbers.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			data, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read report: %w", err)
			}
			files, err := coverage.Parse(data, coverage.Format(format))
			if err != nil {
				return err
			}
			if len(files) == 0 {
				return fmt.Errorf("no file coverage found in %s", args[0])
			}

			absRoot, err := filepath.Abs(repoPath)
			if err != nil {
				return err
			}
			for i := range files {
				files[i].Path = normalizeCoveragePath(absRoot, stripPrefix, files[i].Path)
			}

			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := resolveReadRepo(ctx, conn, readRepoOptions{Path: repoPath, ID: repoID})
			if err != nil {
				return err
			}
			if err := coveragestore.New(conn).Upsert(ctx, repoRow.ID, commitSHA, files); err != nil {
				return fmt.Errorf("store coverage: %w", err)
			}

			var sum float64
			for _, f := range files {
				sum += f.LinePct
			}
			fmt.Fprintf(cmd.OutOrStdout(), "imported coverage for %d file(s), avg line coverage %.1f%% (format=%s)\n",
				len(files), sum/float64(len(files)), files[0].Format)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	cmd.Flags().StringVar(&format, "format", "", "report format: lcov|cobertura (default: auto-detect)")
	cmd.Flags().StringVar(&stripPrefix, "strip-prefix", "", "leading path segment to strip from report file paths")
	cmd.Flags().StringVar(&commitSHA, "commit", "", "commit SHA the report was generated against (provenance)")
	return cmd
}

func newCoverageStatusCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show how many files have stored coverage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := resolveReadRepo(ctx, conn, readRepoOptions{Path: repoPath, ID: repoID})
			if err != nil {
				return err
			}
			store := coveragestore.New(conn)
			n, err := store.Count(ctx, repoRow.ID)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if n == 0 {
				fmt.Fprintln(out, "no coverage imported — run `repowise coverage import <report>`")
				return nil
			}
			cov, _ := store.CoverageMap(ctx, repoRow.ID)
			var sum float64
			for _, p := range cov {
				sum += p
			}
			fmt.Fprintf(out, "%d file(s) with coverage, avg line coverage %.1f%%\n", n, sum/float64(len(cov)))
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	return cmd
}

// normalizeCoveragePath converts a report file path to repo-relative POSIX
// form. It strips an optional caller-supplied prefix, then makes absolute
// paths relative to the repo root when they fall under it.
func normalizeCoveragePath(absRoot, stripPrefix, p string) string {
	p = filepath.ToSlash(p)
	if stripPrefix != "" {
		p = strings.TrimPrefix(p, filepath.ToSlash(stripPrefix))
	}
	p = strings.TrimPrefix(p, "./")
	if filepath.IsAbs(p) {
		if rel, err := filepath.Rel(absRoot, filepath.FromSlash(p)); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return strings.TrimPrefix(p, "/")
}
