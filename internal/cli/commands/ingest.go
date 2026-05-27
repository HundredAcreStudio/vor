package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/ingestion/graph"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser"

	// Side-effect imports: each per-language parser package registers itself
	// with the parser registry in its init(). Add languages here as they
	// land.
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/golang"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/python"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/typescript"

	"github.com/repowise-dev/repowise-go/internal/ingestion/traverser"
	"github.com/repowise-dev/repowise-go/internal/persistence/graphstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

// newIngestCmd registers `repowise ingest`. The pipeline grows in
// concentric layers:
//
//   - default:   walk + per-skip-reason stats
//   - --list:    enumerate included files
//   - --parse:   per-file AST extraction summary
//   - --graph:   parse + build the dependency graph + metrics + summary
//   - --persist: --graph plus write the graph to the configured DB
func newIngestCmd() *cobra.Command {
	var (
		listFiles bool
		maxKB     int
		extraExcl []string
		showStats bool
		parseAST  bool
		buildGraph bool
		persist   bool
	)
	cmd := &cobra.Command{
		Use:   "ingest [PATH]",
		Short: "Walk a repository, parse it, optionally build + persist the graph",
		Args:  cobra.MaximumNArgs(1),
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

			tr, err := traverser.New(traverser.Options{
				RepoRoot:             absRoot,
				MaxFileSizeKB:        maxKB,
				ExtraExcludePatterns: extraExcl,
			})
			if err != nil {
				return err
			}

			files, stats, err := tr.Collect(ctx)
			if err != nil && err != context.Canceled {
				return fmt.Errorf("walk: %w", err)
			}

			// --persist implies --graph implies --parse.
			if persist {
				buildGraph = true
			}
			if buildGraph {
				parseAST = true
			}

			out := cmd.OutOrStdout()
			ap := parser.New()
			parseSummary := parseAggregate{}
			var parsedFiles []models.ParsedFile

			if listFiles || parseAST {
				for _, f := range files {
					line := fmt.Sprintf(" %-12s %s", f.Language, f.Path)
					if f.IsEntryPoint {
						line = "*" + line[1:]
					}
					if parseAST {
						parsed, summary, err := parseOne(ctx, ap, f)
						if err != nil {
							fmt.Fprintf(out, "%s    [parse error: %v]\n", line, err)
							continue
						}
						if !summary.Skipped {
							parsedFiles = append(parsedFiles, parsed)
						}
						line += fmt.Sprintf("    [symbols=%d imports=%d calls=%d]",
							summary.Symbols, summary.Imports, summary.Calls)
						parseSummary.add(summary)
					}
					if listFiles {
						fmt.Fprintln(out, line)
					}
				}
			}

			if showStats || (!listFiles && !parseAST) {
				printStats(out, stats)
			}

			if parseAST {
				fmt.Fprintf(out, "\nAST summary: symbols=%d imports=%d calls=%d (parsed %d / skipped %d / errored %d)\n",
					parseSummary.Symbols, parseSummary.Imports, parseSummary.Calls,
					parseSummary.Parsed, parseSummary.Skipped, parseSummary.Errored)
			}

			if buildGraph {
				b := graph.NewBuilder(nil, graph.Options{})
				for _, p := range parsedFiles {
					b.AddFile(p)
				}
				g := b.Build()
				g.ComputeMetrics()
				printGraphSummary(out, g)

				if persist {
					if err := persistGraph(ctx, root, absRoot, g); err != nil {
						return fmt.Errorf("persist: %w", err)
					}
					fmt.Fprintf(out, "\npersisted to %s\n", databaseTarget(root))
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&listFiles, "list", false, "list each included file (default: stats only)")
	cmd.Flags().BoolVar(&showStats, "stats", false, "also print summary stats when --list is set")
	cmd.Flags().BoolVar(&parseAST, "parse", false, "parse each file's AST and report symbol/import/call counts")
	cmd.Flags().BoolVar(&buildGraph, "graph", false, "build the dependency graph + metrics (implies --parse)")
	cmd.Flags().BoolVar(&persist, "persist", false, "persist the graph to the database (implies --graph)")
	cmd.Flags().IntVar(&maxKB, "max-kb", 0, "max file size in KB (0 = default 500)")
	cmd.Flags().StringArrayVar(&extraExcl, "exclude", nil, "extra gitignore-syntax patterns to skip (repeatable)")
	return cmd
}

type fileParseSummary struct {
	Symbols, Imports, Calls int
	Skipped                 bool
}

type parseAggregate struct {
	Symbols, Imports, Calls  int
	Parsed, Skipped, Errored int
}

func (a *parseAggregate) add(s fileParseSummary) {
	a.Symbols += s.Symbols
	a.Imports += s.Imports
	a.Calls += s.Calls
	if s.Skipped {
		a.Skipped++
	} else {
		a.Parsed++
	}
}

func parseOne(ctx context.Context, ap *parser.ASTParser, fi models.FileInfo) (models.ParsedFile, fileParseSummary, error) {
	if parser.LookupParser(fi.Language) == nil {
		return models.ParsedFile{}, fileParseSummary{Skipped: true}, nil
	}
	src, err := os.ReadFile(fi.AbsPath)
	if err != nil {
		return models.ParsedFile{}, fileParseSummary{}, err
	}
	parsed, err := ap.Parse(ctx, fi, src)
	if err != nil {
		return models.ParsedFile{}, fileParseSummary{}, err
	}
	return parsed, fileParseSummary{
		Symbols: len(parsed.Symbols),
		Imports: len(parsed.Imports),
		Calls:   len(parsed.Calls),
	}, nil
}

func printStats(w io.Writer, s models.TraversalStats) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintf(tw, "included\t%d\n", s.Included)
	fmt.Fprintf(tw, "total-walked\t%d\n", s.TotalPathsWalked)
	fmt.Fprintf(tw, "skipped (gitignore)\t%d\n", s.SkippedGitignore)
	fmt.Fprintf(tw, "skipped (repowiseIgnore)\t%d\n", s.SkippedExtraIgnore)
	fmt.Fprintf(tw, "skipped (oversized)\t%d\n", s.SkippedOversized)
	fmt.Fprintf(tw, "skipped (binary)\t%d\n", s.SkippedBinary)
	fmt.Fprintf(tw, "skipped (generated)\t%d\n", s.SkippedGenerated)
	fmt.Fprintf(tw, "skipped (blocked ext)\t%d\n", s.SkippedBlockedExtension)
	fmt.Fprintf(tw, "skipped (blocked pattern)\t%d\n", s.SkippedBlockedPattern)
	fmt.Fprintf(tw, "skipped (unknown lang)\t%d\n", s.SkippedUnknownLanguage)
	if len(s.LangCounts) > 0 {
		fmt.Fprintln(tw)
		for lang, n := range s.LangCounts {
			fmt.Fprintf(tw, "  %s\t%d\n", lang, n)
		}
	}
}

func printGraphSummary(w io.Writer, g *graph.Graph) {
	fmt.Fprintf(w, "\nGraph: %d nodes, %d edges\n", g.NodeCount(), g.EdgeCount())
	counts := g.CountByEdgeType()
	if len(counts) == 0 {
		return
	}
	// Sort edge types for stable output.
	types := make([]string, 0, len(counts))
	for t := range counts {
		types = append(types, string(t))
	}
	sort.Strings(types)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	for _, t := range types {
		fmt.Fprintf(tw, "  %s\t%d\n", t, counts[models.EdgeType(t)])
	}

	// Top-5 most-PageRanked nodes — gives a quick sanity check that the
	// graph is non-degenerate.
	nodes := g.Nodes()
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].PageRank > nodes[j].PageRank })
	top := 5
	if len(nodes) < top {
		top = len(nodes)
	}
	fmt.Fprintln(tw, "\nTop nodes (by PageRank):")
	for i := 0; i < top; i++ {
		fmt.Fprintf(tw, "  %.4f\t%s\n", nodes[i].PageRank, nodes[i].StringID)
	}
}

// persistGraph opens the configured DB, runs pending migrations (so the
// caller doesn't have to `db migrate` first), ensures the repository row,
// and writes the graph snapshot.
func persistGraph(ctx context.Context, repoArg, absRoot string, g *graph.Graph) error {
	conn, dialect, err := openDB(ctx, repoArg)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := migrations.Up(ctx, conn, dialect); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, absRoot, "")
	if err != nil {
		return fmt.Errorf("ensure repository: %w", err)
	}
	return graphstore.New(conn).ReplaceGraph(ctx, repoRow.ID, g)
}

func databaseTarget(repoArg string) string {
	abs, err := filepath.Abs(filepath.Join(repoArg, ".repowise", "wiki.db"))
	if err != nil {
		return repoArg + "/.repowise/wiki.db"
	}
	return abs
}
