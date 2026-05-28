package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/logging"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/pipeline"
	"github.com/HundredAcreStudio/vor/internal/userconfig"

	// Side-effect imports: ingest's registry hooks need to fire here too.
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/cargo"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/gomod"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/graphql"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/npm"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/nuget"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/openapi"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/protobuf"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/external/pypi"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/cpp"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/csharp"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/golang"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/java"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/javascript"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/kotlin"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/luau"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/php"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/python"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/ruby"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/rust"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/scala"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/swift"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/typescript"
)

// newInitCmd runs the full ingest pipeline through the tracked
// orchestrator. Distinct from `ingest --persist` in that every phase
// lands in pipeline_jobs for observability + future resume.
func newInitCmd() *cobra.Command {
	var (
		repoPath        string
		gitMaxCommits   int
		useGlobalConfig bool
	)
	cmd := &cobra.Command{
		Use:   "init [PATH]",
		Short: "Index a repository through the tracked pipeline (full INIT run)",
		Long: `Runs every phase of the analysis pipeline — traverse → parse →
git → graph → deadcode → health → externals → persist — recording
each in pipeline_jobs.

Equivalent to 'vor ingest --persist' but with phase tracking
for observability. Use 'vor pipeline log' (forthcoming) to
inspect the most recent runs.`,
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

			conn, dialect, err := openDB(ctx, root)
			if err != nil {
				return err
			}
			defer conn.Close()
			if err := migrations.Up(ctx, conn, dialect); err != nil {
				return fmt.Errorf("apply migrations: %w", err)
			}
			repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, absRoot, "")
			if err != nil {
				return fmt.Errorf("ensure repo: %w", err)
			}

			// Scaffold a commented config on first init so the available
			// knobs are discoverable. --use-global-config targets the
			// machine-wide ~/.config/vor/config.yaml (for a daemon serving
			// many repos); otherwise the repo-local .vor/config.yaml. Either
			// way, an existing file is never clobbered.
			if useGlobalConfig {
				switch created, path, err := scaffoldGlobalConfig(); {
				case err != nil:
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not scaffold global config: %v\n", err)
				case created:
					fmt.Fprintf(cmd.OutOrStdout(), "wrote starter global config at %s\n", path)
				default:
					fmt.Fprintf(cmd.OutOrStdout(), "global config already exists at %s (left unchanged)\n", path)
				}
			} else if created, path := scaffoldRepoConfig(absRoot); created {
				fmt.Fprintf(cmd.OutOrStdout(), "wrote starter config at %s\n", path)
			}

			logger := logging.New(logging.Options{
				Format: logging.FormatAuto,
				Level:  logging.ParseLevel("info"),
				Out:    os.Stderr,
			})

			result, err := pipeline.Run(ctx, pipeline.Options{
				RepoPath:      absRoot,
				Mode:          pipeline.ModeInit,
				DB:            conn,
				RepositoryID:  repoRow.ID,
				Logger:        logger,
				GitMaxCommits: gitMaxCommits,
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				printRunSummary(cmd, result, true)
				return err
			}
			printRunSummary(cmd, result, false)

			// Auto-regenerate CLAUDE.md from the freshly-indexed state.
			// Mirrors the Python flow: every init/update produces a
			// fresh managed block in <repo>/CLAUDE.md without touching
			// content above the VOR:START marker. No LLM calls —
			// just structured SQL queries.
			if mdErr := regenerateClaudeMd(ctx, conn, repoRow.ID, absRoot); mdErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: CLAUDE.md auto-regen skipped: %v\n", mdErr)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "CLAUDE.md regenerated at %s\n",
					filepath.Join(absRoot, "CLAUDE.md"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "(deprecated; pass PATH positionally instead)")
	cmd.Flags().IntVar(&gitMaxCommits, "git-max-commits", 0, "cap commits walked by the git phase (0 = default 10000)")
	cmd.Flags().BoolVar(&useGlobalConfig, "use-global-config", false, "scaffold the machine-wide ~/.config/vor/config.yaml instead of the repo-local .vor/config.yaml")
	return cmd
}

// repoConfigTemplate is the commented starter config written on first init.
// Every key is commented out, so the file changes no behaviour until the
// user edits it — it exists for discoverability.
const repoConfigTemplate = `# vor configuration (repo-local).
# Merged on top of ~/.config/vor/config.yaml, then VOR_* env vars.
# Everything here is optional — uncomment what you need.

# provider: anthropic        # anthropic | openai | google | ollama | litellm | mock
# model: claude-sonnet-4-6

# Code-health rule overrides. Each rule matches files by 'pattern'
# (gitignore-syntax glob) or 'path' (prefix), and sets per-biomarker
# actions under 'overrides'. Use "disabled" to suppress a check; the "*"
# key applies to every biomarker. Rules are additive with the global
# config's rules.
# health_rules:
#   - pattern: "**/*_test.go"
#     overrides:
#       high_complexity: disabled
#       long_function: disabled
#   - path: "internal/generated/"
#     overrides:
#       "*": disabled
`

// globalConfigTemplate is the commented starter written to the machine-wide
// ~/.config/vor/config.yaml by `vor init --use-global-config`. It uses the
// same key shape the global layer is parsed with (config.Config), so the
// documented knobs take effect once uncommented. Geared toward a daemon
// serving many repos: set defaults here, override per-repo locally.
const globalConfigTemplate = `# vor user-global configuration.
# Lives at ~/.config/vor/config.yaml (or $XDG_CONFIG_HOME/vor/config.yaml).
# Applies to every repo on this machine, merged UNDER each repo's
# .vor/config.yaml and VOR_* env vars (repo-local + env take precedence).
# Everything here is optional — uncomment what you need.

# provider: anthropic        # anthropic | openai | google | ollama | litellm | mock
# model: claude-sonnet-4-6

# The 'vor serve' daemon tracks whatever repos you 'vor register'; that
# membership lives in the shared database, not here. This file is for
# settings (provider/model/watch defaults) only.

# Auto-reindex behaviour for 'vor serve'. Useful for a daemon tracking
# multiple repos: set the machine-wide default here, then override it in an
# individual repo's .vor/config.yaml (watch.enabled / watch.debounce).
# watch:
#   enabled: true            # set false to disable auto-reindex by default
#   debounce: 1.5s           # quiet period after edits before a reindex fires
`

// scaffoldGlobalConfig writes globalConfigTemplate to the user-global
// config path when that file does not yet exist. Returns whether it created
// the file, the path, and any error. Never clobbers an existing file.
func scaffoldGlobalConfig() (bool, string, error) {
	path, err := userconfig.ConfigPath() // creates the config dir as a side effect
	if err != nil {
		return false, "", err
	}
	if _, err := os.Stat(path); err == nil {
		return false, path, nil // already exists — never clobber
	} else if !os.IsNotExist(err) {
		return false, path, err
	}
	if err := os.WriteFile(path, []byte(globalConfigTemplate), 0o644); err != nil {
		return false, path, err
	}
	return true, path, nil
}

// scaffoldRepoConfig writes repoConfigTemplate to <root>/.vor/config.yaml
// when that file does not yet exist. Returns whether it created the file and
// the path. All errors are swallowed — a missing starter config is harmless.
func scaffoldRepoConfig(absRoot string) (bool, string) {
	path := filepath.Join(absRoot, ".vor", "config.yaml")
	if _, err := os.Stat(path); err == nil {
		return false, path // already exists — never clobber
	} else if !os.IsNotExist(err) {
		return false, path
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, path
	}
	if err := os.WriteFile(path, []byte(repoConfigTemplate), 0o644); err != nil {
		return false, path
	}
	return true, path
}

func printRunSummary(cmd *cobra.Command, r *pipeline.Result, failed bool) {
	if r == nil {
		return
	}
	out := cmd.OutOrStdout()
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "  phase\tstate\tduration")
	for _, p := range r.Phases {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", p.Phase, p.State, p.Duration)
	}
	if failed {
		fmt.Fprintln(out, "\npipeline halted on failure; see logs above")
		return
	}
	fmt.Fprintf(out, "\n%d files indexed, %d graph nodes, %d health findings\n",
		r.TraversalStats.Included, r.Graph.NodeCount(), len(r.HealthResult.Findings))
}
