package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/analysis/security"
	"github.com/repowise-dev/repowise-go/internal/ingestion/traverser"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/persistence/securitystore"
)

// newSecurityCmd groups the pattern-based security scanner.
func newSecurityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "security",
		Short: "Pattern-based security scan (secrets, weak crypto, injection sinks)",
	}
	cmd.AddCommand(newSecurityScanCmd())
	cmd.AddCommand(newSecurityListCmd())
	return cmd
}

func newSecurityScanCmd() *cobra.Command {
	var (
		repoPath string
		maxKB    int
	)
	cmd := &cobra.Command{
		Use:   "scan [PATH]",
		Short: "Scan the repo for security findings and store them",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root := repoPath
			if len(args) > 0 {
				root = args[0]
			}
			absRoot, err := filepath.Abs(root)
			if err != nil {
				return err
			}
			tr, err := traverser.New(traverser.Options{RepoRoot: absRoot, MaxFileSizeKB: maxKB})
			if err != nil {
				return err
			}
			files, _, err := tr.Collect(ctx)
			if err != nil && err != ctx.Err() {
				return fmt.Errorf("walk: %w", err)
			}

			var findings []security.Finding
			for _, f := range files {
				data, err := os.ReadFile(f.AbsPath)
				if err != nil {
					continue // unreadable file — skip, don't fail the whole scan
				}
				findings = append(findings, security.Scan(f.Path, data)...)
			}

			conn, _, err := openDB(ctx, root)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, absRoot, "")
			if err != nil {
				return fmt.Errorf("resolve repo: %w", err)
			}
			if err := securitystore.New(conn).ReplaceAll(ctx, repoRow.ID, findings); err != nil {
				return fmt.Errorf("store findings: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "scanned %d file(s): %s\n", len(files), severityBreakdown(findings))
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (overridden by positional PATH)")
	cmd.Flags().IntVar(&maxKB, "max-kb", 0, "max file size in KB to scan (0 = default 500)")
	return cmd
}

func newSecurityListCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
		severity string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List stored security findings",
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
			findings, err := securitystore.New(conn).List(ctx, repoRow.ID)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(findings) == 0 {
				fmt.Fprintln(out, "no security findings — run `repowise security scan`")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  severity\tkind\tfile:line\tsnippet")
			for _, f := range findings {
				if severity != "" && f.Severity != severity {
					continue
				}
				fmt.Fprintf(tw, "  %s\t%s\t%s:%d\t%s\n", f.Severity, f.Kind, f.FilePath, f.Line, f.Snippet)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	cmd.Flags().StringVar(&severity, "severity", "", "filter by severity (critical|high|medium|low)")
	return cmd
}

// severityBreakdown renders "3 findings (1 critical, 2 high)".
func severityBreakdown(fs []security.Finding) string {
	if len(fs) == 0 {
		return "no findings"
	}
	counts := map[string]int{}
	for _, f := range fs {
		counts[f.Severity]++
	}
	order := []string{security.SeverityCritical, security.SeverityHigh, security.SeverityMedium, security.SeverityLow}
	var parts []string
	for _, sev := range order {
		if counts[sev] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[sev], sev))
		}
	}
	return fmt.Sprintf("%d finding(s) (%s)", len(fs), joinComma(parts))
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
