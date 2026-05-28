package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/logging"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/pipeline"

	// Side-effect imports: ingest's registry hooks need to fire here too.
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/cargo"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/gomod"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/graphql"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/npm"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/nuget"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/openapi"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/protobuf"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/pypi"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/cpp"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/csharp"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/golang"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/java"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/javascript"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/kotlin"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/luau"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/php"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/python"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/ruby"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/rust"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/scala"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/swift"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/typescript"
)

// newInitCmd runs the full ingest pipeline through the tracked
// orchestrator. Distinct from `ingest --persist` in that every phase
// lands in pipeline_jobs for observability + future resume.
func newInitCmd() *cobra.Command {
	var (
		repoPath      string
		gitMaxCommits int
	)
	cmd := &cobra.Command{
		Use:   "init [PATH]",
		Short: "Index a repository through the tracked pipeline (full INIT run)",
		Long: `Runs every phase of the analysis pipeline — traverse → parse →
git → graph → deadcode → health → externals → persist — recording
each in pipeline_jobs.

Equivalent to 'repowise ingest --persist' but with phase tracking
for observability. Use 'repowise pipeline log' (forthcoming) to
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
			// fresh managed block under <repo>/.claude/CLAUDE.md
			// without touching content above the REPOWISE:START
			// marker. No LLM calls — just structured SQL queries.
			if mdErr := regenerateClaudeMd(ctx, conn, repoRow.ID, absRoot); mdErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: CLAUDE.md auto-regen skipped: %v\n", mdErr)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "CLAUDE.md regenerated at %s\n",
					filepath.Join(absRoot, ".claude", "CLAUDE.md"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "(deprecated; pass PATH positionally instead)")
	cmd.Flags().IntVar(&gitMaxCommits, "git-max-commits", 0, "cap commits walked by the git phase (0 = default 10000)")
	return cmd
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
