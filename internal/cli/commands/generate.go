package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/config"
	"github.com/repowise-dev/repowise-go/internal/generation/models"
	"github.com/repowise-dev/repowise-go/internal/generation/runner"
	"github.com/repowise-dev/repowise-go/internal/persistence/coststore"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/providers"
	"github.com/repowise-dev/repowise-go/internal/providers/middleware"
	"github.com/repowise-dev/repowise-go/internal/providers/ratelimit"

	// Side-effect imports — register every provider + embedder so
	// --provider / config.embedder can pick any of them at runtime.
	_ "github.com/repowise-dev/repowise-go/internal/providers/anthropic"
	_ "github.com/repowise-dev/repowise-go/internal/providers/google"
	_ "github.com/repowise-dev/repowise-go/internal/providers/litellm"
	_ "github.com/repowise-dev/repowise-go/internal/providers/mock"
	_ "github.com/repowise-dev/repowise-go/internal/providers/ollama"
	_ "github.com/repowise-dev/repowise-go/internal/providers/openai"
)

// newGenerateCmd produces wiki pages from the persisted ingest state.
// Deliberately separate from `init` so users control LLM spend.
func newGenerateCmd() *cobra.Command {
	var (
		repoPath     string
		target       string
		providerName string
		model        string
		kindsCSV     string
		limit        int
		force        bool
		dryRun       bool
	)
	cmd := &cobra.Command{
		Use:   "generate [PATH]",
		Short: "Generate wiki pages for indexed files",
		Long: `Walks the latest indexed graph and generates one wiki page per
file using the configured LLM provider. Pages with matching source_hash
are skipped unless --force is set. Use --dry-run to preview without
spending tokens.

Provider defaults come from .repowise/config.yaml or environment
variables; override per-invocation with --provider and --model.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root := repoPath
			if len(args) > 0 {
				root = args[0]
			}
			absRoot, err := filepath.Abs(root)
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}

			kinds, err := parsePageKinds(kindsCSV)
			if err != nil {
				return err
			}

			cfg, err := config.Load(root)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if providerName == "" {
				providerName = cfg.Provider
			}
			if model == "" {
				model = cfg.Model
			}

			conn, _, err := openDB(ctx, root)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, absRoot, "")
			if err != nil {
				return fmt.Errorf("ensure repo: %w", err)
			}

			var prov providers.Provider
			if !dryRun {
				raw, err := buildProvider(providerName, model, cfg)
				if err != nil {
					return fmt.Errorf("build provider %q: %w", providerName, err)
				}
				// Wrap real providers with the cost/ratelimit/retry chain.
				// The mock provider is exempted because: it never produces
				// transient errors (retry is dead weight), never costs
				// anything (cost rows would be all-zero), and the spend
				// totals are most useful when they're real.
				if providerName == "mock" {
					prov = raw
				} else {
					prov = middleware.Wrap(raw, middleware.Options{
						RepositoryID: repoRow.ID,
						CostStore:    coststore.New(conn),
						Limiter:      ratelimit.New(cfg.RPM, cfg.TPM),
					})
				}
			}

			out := cmd.OutOrStdout()
			summary, err := runner.Run(ctx, runner.Options{
				RepoRoot:     absRoot,
				RepositoryID: repoRow.ID,
				DB:           conn,
				Provider:     prov,
				Model:        model,
				Kinds:        kinds,
				Target:       target,
				Limit:        limit,
				Force:        force,
				DryRun:       dryRun,
				OnProgress: func(r runner.FileResult) {
					fmt.Fprintf(out, "  %-10s  %-50s  %s\n", r.Status, r.Path, r.Reason)
				},
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "\n%d generated, %d skipped, %d missing, %d errors\n",
				summary.GeneratedCount, summary.SkippedCount, summary.MissingCount, summary.ErrorCount)
			if summary.GeneratedCount > 0 {
				fmt.Fprintf(out, "tokens: in=%d  out=%d  cached=%d\n",
					summary.TotalInputTokens, summary.TotalOutputTokens, summary.TotalCachedTokens)
			}
			if summary.DryRunCount > 0 {
				fmt.Fprintf(out, "[dry run] %d files would be generated\n", summary.DryRunCount)
			}
			if len(summary.Files) == 0 {
				fmt.Fprintln(out, "no indexed files found (run `repowise init` first)")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (overridden by positional PATH)")
	cmd.Flags().StringVar(&target, "target", "", "limit generation to a single file (repo-relative)")
	cmd.Flags().StringVar(&providerName, "provider", "", "LLM provider name (defaults to config.provider)")
	cmd.Flags().StringVar(&model, "model", "", "model identifier (defaults to config.model)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max pages to generate this run (0 = no cap)")
	cmd.Flags().BoolVar(&force, "force", false, "regenerate even when source_hash matches")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be generated without calling the provider")
	cmd.Flags().StringVar(&kindsCSV, "kind", "file_overview", "comma-separated page kinds: file_overview|directory_overview|symbol_detail")
	return cmd
}

// parsePageKinds validates the --kind flag value. Returns an error
// rather than silently filtering so a typo doesn't become "ran a no-op".
func parsePageKinds(csv string) ([]models.PageKind, error) {
	if csv == "" {
		return nil, nil
	}
	known := map[string]models.PageKind{
		"file_overview":      models.PageKindFileOverview,
		"directory_overview": models.PageKindDirectoryOverview,
		"symbol_detail":      models.PageKindSymbolDetail,
	}
	out := []models.PageKind{}
	seen := map[models.PageKind]bool{}
	for _, raw := range strings.Split(csv, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		k, ok := known[name]
		if !ok {
			return nil, fmt.Errorf("unknown page kind %q (valid: file_overview, directory_overview, symbol_detail)", name)
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out, nil
}

// buildOptionalProvider builds the configured provider for read-side
// daemons (mcp / serve) where LLM access is a bonus, not a
// requirement. Returns (nil, nil) when the provider can't be built
// (e.g. no API key) so the synthesis tools degrade gracefully rather
// than refusing to start. The mock provider is never auto-wired here —
// a deterministic stub would make get_answer return nonsense; better
// to advertise "no synthesis" honestly.
func buildOptionalProvider(cfg config.Config) (providers.Provider, string) {
	if cfg.Provider == "" || cfg.Provider == "mock" {
		return nil, ""
	}
	prov, err := buildProvider(cfg.Provider, cfg.Model, cfg)
	if err != nil {
		return nil, ""
	}
	return prov, cfg.Model
}

// buildProvider hydrates a Provider with config-derived options. For now
// only api_key is plumbed through — base_url / version are advanced
// knobs the YAML config doesn't expose yet.
func buildProvider(name, model string, cfg config.Config) (providers.Provider, error) {
	opts := providers.Options{}
	switch name {
	case "anthropic":
		key := cfg.ProviderKeys.Anthropic
		if key == "" {
			key = os.Getenv("ANTHROPIC_API_KEY")
		}
		if key == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY is required for the anthropic provider")
		}
		opts["api_key"] = key
		if model != "" {
			opts["default_model"] = model
		}
	case "openai":
		key := firstSet(cfg.ProviderKeys.OpenAI, os.Getenv("OPENAI_API_KEY"))
		if key == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is required for the openai provider")
		}
		opts["api_key"] = key
		if b := os.Getenv("REPOWISE_OPENAI_BASE_URL"); b != "" {
			opts["base_url"] = b
		}
		if model != "" {
			opts["default_model"] = model
		}
	case "google":
		key := firstSet(cfg.ProviderKeys.Gemini, os.Getenv("GEMINI_API_KEY"), os.Getenv("GOOGLE_API_KEY"))
		if key == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY or GOOGLE_API_KEY is required for the google provider")
		}
		opts["api_key"] = key
		if model != "" {
			opts["default_model"] = model
		}
	case "ollama":
		// Local server — no key. Base URL overridable for remote hosts.
		if b := os.Getenv("REPOWISE_OLLAMA_BASE_URL"); b != "" {
			opts["base_url"] = b
		}
		if model != "" {
			opts["default_model"] = model
		}
	case "litellm":
		base := os.Getenv("REPOWISE_LITELLM_BASE_URL")
		if base == "" {
			return nil, fmt.Errorf("REPOWISE_LITELLM_BASE_URL is required for the litellm provider")
		}
		opts["base_url"] = base
		if key := firstSet(cfg.ProviderKeys.OpenRouter, os.Getenv("LITELLM_API_KEY")); key != "" {
			opts["api_key"] = key
		}
		if model != "" {
			opts["default_model"] = model
		}
	case "mock":
		if model != "" {
			opts["model"] = model
		}
	}
	return providers.NewProvider(name, opts)
}

// firstSet returns the first non-empty string, or "".
func firstSet(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
