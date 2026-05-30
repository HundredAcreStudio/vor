package commands

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/config"
	gmodels "github.com/HundredAcreStudio/vor/internal/generation/models"
	"github.com/HundredAcreStudio/vor/internal/generation/runner"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

// newGenerateCmd is the one-off, manual trigger for wiki page generation. It
// runs the same runner the post-pipeline wiki task uses, but on demand and
// regardless of the repo's task enablement toggle — invoking it is explicit
// intent. The repo must already be indexed (run `vor init` first).
func newGenerateCmd() *cobra.Command {
	var (
		kindsFlag        []string
		target           string
		limit            int
		force            bool
		dryRun           bool
		providerOverride string
		modelOverride    string
	)
	cmd := &cobra.Command{
		Use:   "generate [PATH]",
		Short: "Generate wiki pages (file/directory/symbol overviews) for an indexed repo",
		Long: `Generates LLM-written overview pages for an already-indexed repository.

Only units whose source has changed since the last generation are
rewritten, unless --force is given. Requires an LLM provider to be
configured (provider + API key) or pass --provider/--model.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root := "."
			if len(args) > 0 {
				root = args[0]
			}
			absRoot, err := filepath.Abs(root)
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}

			conn, _, err := openDB(ctx, root)
			if err != nil {
				return err
			}
			defer conn.Close()

			repo, err := repos.New(conn).GetByLocalPath(ctx, absRoot)
			if err != nil {
				return fmt.Errorf("locate repo: %w", err)
			}
			if repo == nil {
				return fmt.Errorf("%s is not indexed yet; run 'vor init' first", absRoot)
			}

			cfg, err := config.Resolve(ctx, conn, repo.ID, config.LoadBootstrap())
			if err != nil {
				return fmt.Errorf("resolve config: %w", err)
			}
			if providerOverride != "" {
				cfg.Provider = providerOverride
			}
			if modelOverride != "" {
				cfg.Model = modelOverride
			}

			kinds, err := parsePageKinds(kindsFlag)
			if err != nil {
				return err
			}

			provider, model := buildOptionalProvider(cfg)
			if provider == nil && !dryRun {
				return fmt.Errorf("no LLM provider configured; set 'provider' (and an API key) in config, or pass --provider/--model")
			}

			out := cmd.OutOrStdout()
			sum, err := runner.Run(ctx, runner.Options{
				RepoRoot:     absRoot,
				RepositoryID: repo.ID,
				DB:           conn,
				Provider:     provider,
				Model:        model,
				Kinds:        kinds,
				Target:       target,
				Limit:        limit,
				Force:        force,
				DryRun:       dryRun,
				OnProgress: func(fr runner.FileResult) {
					reason := ""
					if fr.Reason != "" {
						reason = " — " + fr.Reason
					}
					fmt.Fprintf(out, "  %-9s %s%s\n", fr.Status, fr.Path, reason)
				},
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "\n%d generated, %d skipped, %d errors, %d missing%s\n",
				sum.GeneratedCount, sum.SkippedCount, sum.ErrorCount, sum.MissingCount,
				dryRunNote(dryRun, sum.DryRunCount))
			fmt.Fprintf(out, "tokens: %d in, %d out, %d cached\n",
				sum.TotalInputTokens, sum.TotalOutputTokens, sum.TotalCachedTokens)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&kindsFlag, "kinds", nil,
		"page kinds to generate: file_overview, directory_overview, symbol_detail (default: all)")
	cmd.Flags().StringVar(&target, "target", "", "restrict to a single file path or symbol id")
	cmd.Flags().IntVar(&limit, "limit", 0, "cap number of pages generated (0 = no cap)")
	cmd.Flags().BoolVar(&force, "force", false, "regenerate even when the existing page is fresh")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list what would be generated without calling the provider")
	cmd.Flags().StringVar(&providerOverride, "provider", "", "override the configured LLM provider")
	cmd.Flags().StringVar(&modelOverride, "model", "", "override the configured model")
	return cmd
}

func dryRunNote(dryRun bool, n int) string {
	if dryRun {
		return fmt.Sprintf(" (dry-run: %d would generate)", n)
	}
	return ""
}

// parsePageKinds maps CLI kind strings to PageKinds. Empty input defaults to
// the three implemented kinds.
func parsePageKinds(in []string) ([]gmodels.PageKind, error) {
	if len(in) == 0 {
		return []gmodels.PageKind{
			gmodels.PageKindArchitecture,
			gmodels.PageKindFileOverview,
			gmodels.PageKindDirectoryOverview,
			gmodels.PageKindSymbolDetail,
		}, nil
	}
	valid := map[string]gmodels.PageKind{
		string(gmodels.PageKindArchitecture):      gmodels.PageKindArchitecture,
		string(gmodels.PageKindFileOverview):      gmodels.PageKindFileOverview,
		string(gmodels.PageKindDirectoryOverview): gmodels.PageKindDirectoryOverview,
		string(gmodels.PageKindSymbolDetail):      gmodels.PageKindSymbolDetail,
	}
	out := make([]gmodels.PageKind, 0, len(in))
	for _, s := range in {
		k, ok := valid[strings.TrimSpace(s)]
		if !ok {
			return nil, fmt.Errorf("unknown page kind %q (valid: architecture, file_overview, directory_overview, symbol_detail)", s)
		}
		out = append(out, k)
	}
	return out, nil
}
