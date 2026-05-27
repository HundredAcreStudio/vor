package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser"

	// Side-effect imports: each per-language parser package registers itself
	// with the parser registry in its init(). Add languages here as they
	// land.
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/golang"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/python"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/parser/typescript"

	"github.com/repowise-dev/repowise-go/internal/ingestion/traverser"
)

// newIngestCmd registers `repowise ingest`. Phase 2 only exposes the
// traverser (and its statistics); parser + graph build land later this
// phase. The command is shaped to grow into the full pipeline.
func newIngestCmd() *cobra.Command {
	var (
		listFiles bool
		maxKB     int
		extraExcl []string
		showStats bool
		parseAST  bool
	)
	cmd := &cobra.Command{
		Use:   "ingest [PATH]",
		Short: "Walk a repository and report the files repowise would index",
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

			out := cmd.OutOrStdout()
			ap := parser.New()
			parseSummary := parseAggregate{}

			if listFiles || parseAST {
				for _, f := range files {
					line := fmt.Sprintf(" %-12s %s", f.Language, f.Path)
					if f.IsEntryPoint {
						line = "*" + line[1:]
					}
					if parseAST {
						summary, err := parseOne(ctx, ap, f)
						if err != nil {
							fmt.Fprintf(out, "%s    [parse error: %v]\n", line, err)
							continue
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
			return nil
		},
	}
	cmd.Flags().BoolVar(&listFiles, "list", false, "list each included file (default: stats only)")
	cmd.Flags().BoolVar(&showStats, "stats", false, "also print summary stats when --list is set")
	cmd.Flags().BoolVar(&parseAST, "parse", false, "parse each file's AST and report symbol/import/call counts")
	cmd.Flags().IntVar(&maxKB, "max-kb", 0, "max file size in KB (0 = default 500)")
	cmd.Flags().StringArrayVar(&extraExcl, "exclude", nil, "extra gitignore-syntax patterns to skip (repeatable)")
	return cmd
}

type fileParseSummary struct {
	Symbols, Imports, Calls int
	Skipped                 bool
}

type parseAggregate struct {
	Symbols, Imports, Calls   int
	Parsed, Skipped, Errored  int
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

func parseOne(ctx context.Context, ap *parser.ASTParser, fi models.FileInfo) (fileParseSummary, error) {
	if parser.LookupParser(fi.Language) == nil {
		// Either a passthrough language or a parser not yet registered.
		// Don't treat that as an error — just report 0 counts.
		return fileParseSummary{Skipped: true}, nil
	}
	src, err := os.ReadFile(fi.AbsPath)
	if err != nil {
		return fileParseSummary{}, err
	}
	parsed, err := ap.Parse(ctx, fi, src)
	if err != nil {
		return fileParseSummary{}, err
	}
	return fileParseSummary{
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
