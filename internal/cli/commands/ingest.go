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

	"github.com/repowise-dev/repowise-go/internal/analysis/deadcode"
	"github.com/repowise-dev/repowise-go/internal/analysis/health"
	"github.com/repowise-dev/repowise-go/internal/ingestion/external"
	"github.com/repowise-dev/repowise-go/internal/ingestion/external/cargo"
	"github.com/repowise-dev/repowise-go/internal/ingestion/external/gomod"
	"github.com/repowise-dev/repowise-go/internal/ingestion/git"
	"github.com/repowise-dev/repowise-go/internal/ingestion/graph"
	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser"

	// Side-effect imports: each per-language parser package registers itself
	// with the parser registry in its init(). Add languages here as they
	// land.
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

	// Side-effect imports: each manifest extractor registers itself.
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/cargo"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/gomod"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/graphql"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/npm"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/nuget"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/openapi"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/protobuf"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/pypi"

	// Side-effect imports: each per-language import resolver registers
	// itself with the resolver registry.
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/cpp"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/csharp"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/golang"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/java"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/python"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/rust"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/typescript"

	"github.com/repowise-dev/repowise-go/internal/ingestion/traverser"
	"github.com/repowise-dev/repowise-go/internal/persistence/deadstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/externalstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/gitstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/graphstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/healthstore"
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
	o := &ingestOptions{}
	cmd := &cobra.Command{
		Use:   "ingest [PATH]",
		Short: "Walk a repository, parse it, optionally build + persist the graph",
		Args:  cobra.MaximumNArgs(1),
		RunE:  o.run,
	}
	cmd.Flags().BoolVar(&o.listFiles, "list", false, "list each included file (default: stats only)")
	cmd.Flags().BoolVar(&o.showStats, "stats", false, "also print summary stats when --list is set")
	cmd.Flags().BoolVar(&o.parseAST, "parse", false, "parse each file's AST and report symbol/import/call counts")
	cmd.Flags().BoolVar(&o.buildGraph, "graph", false, "build the dependency graph + metrics (implies --parse)")
	cmd.Flags().BoolVar(&o.scanExternals, "externals", false, "scan manifest files for third-party dependencies")
	cmd.Flags().BoolVar(&o.gitIndex, "git", false, "extract per-file git intelligence (hotspots, ownership, co-change)")
	cmd.Flags().IntVar(&o.gitMaxCommits, "git-max-commits", 0, "cap commits walked by --git (0 = default 10000)")
	cmd.Flags().BoolVar(&o.persist, "persist", false, "persist graph + externals + git intelligence (implies --graph --externals --git)")
	cmd.Flags().IntVar(&o.maxKB, "max-kb", 0, "max file size in KB (0 = default 500)")
	cmd.Flags().StringArrayVar(&o.extraExcl, "exclude", nil, "extra gitignore-syntax patterns to skip (repeatable)")
	return cmd
}

// ingestOptions holds the ingest command's flags so the run logic can be
// split into stage helpers instead of one large closure.
type ingestOptions struct {
	listFiles, showStats, parseAST, buildGraph, scanExternals, gitIndex, persist bool
	maxKB, gitMaxCommits                                                         int
	extraExcl                                                                    []string
}

func (o *ingestOptions) run(cmd *cobra.Command, args []string) error {
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
		MaxFileSizeKB:        o.maxKB,
		ExtraExcludePatterns: o.extraExcl,
	})
	if err != nil {
		return err
	}
	files, stats, err := tr.Collect(ctx)
	if err != nil && err != context.Canceled {
		return fmt.Errorf("walk: %w", err)
	}

	// --persist implies --graph + --externals + --git; --graph implies --parse.
	if o.persist {
		o.buildGraph, o.scanExternals, o.gitIndex = true, true, true
	}
	if o.buildGraph {
		o.parseAST = true
	}

	out := cmd.OutOrStdout()
	parsedFiles, summary := o.parseFiles(ctx, out, files)

	if o.showStats || (!o.listFiles && !o.parseAST) {
		printStats(out, stats)
	}
	if o.parseAST {
		fmt.Fprintf(out, "\nAST summary: symbols=%d imports=%d calls=%d (parsed %d / skipped %d / errored %d)\n",
			summary.Symbols, summary.Imports, summary.Calls,
			summary.Parsed, summary.Skipped, summary.Errored)
	}

	// Git intelligence runs first so health.Analyzer can weave hotspot paths
	// into the untested_hotspot biomarker.
	var gitRecords []git.PerFile
	if o.gitIndex {
		gitRecords = o.runGit(ctx, out, absRoot)
	}

	var (
		g            *graph.Graph
		dcFindings   []deadcode.Finding
		healthResult health.Result
	)
	if o.buildGraph {
		g, dcFindings, healthResult = buildIngestGraph(out, absRoot, parsedFiles, gitRecords)
	}

	var extRecords []external.Record
	if o.scanExternals || o.persist {
		records, err := external.ScanRoot(ctx, absRoot)
		if err != nil {
			return fmt.Errorf("scan externals: %w", err)
		}
		extRecords = records
		if o.scanExternals {
			printExternalsSummary(out, records)
		}
	}

	if o.persist {
		if err := persistAll(ctx, root, absRoot, g, extRecords, gitRecords, dcFindings, healthResult); err != nil {
			return fmt.Errorf("persist: %w", err)
		}
		fmt.Fprintf(out, "\npersisted to %s\n", databaseTarget(root))
	}
	return nil
}

// parseFiles handles the --list / --parse loop, returning the parsed files
// and the aggregate counts (empty when neither flag is set). The AST
// summary line is printed by the caller so stats output stays ordered.
func (o *ingestOptions) parseFiles(ctx context.Context, out io.Writer, files []models.FileInfo) ([]models.ParsedFile, parseAggregate) {
	summary := parseAggregate{}
	if !o.listFiles && !o.parseAST {
		return nil, summary
	}
	ap := parser.New()
	var parsedFiles []models.ParsedFile
	for _, f := range files {
		line := fmt.Sprintf(" %-12s %s", f.Language, f.Path)
		if f.IsEntryPoint {
			line = "*" + line[1:]
		}
		if o.parseAST {
			parsed, s, err := parseOne(ctx, ap, f)
			if err != nil {
				fmt.Fprintf(out, "%s    [parse error: %v]\n", line, err)
				continue
			}
			if !s.Skipped {
				parsedFiles = append(parsedFiles, parsed)
			}
			line += fmt.Sprintf("    [symbols=%d imports=%d calls=%d]", s.Symbols, s.Imports, s.Calls)
			summary.add(s)
		}
		if o.listFiles {
			fmt.Fprintln(out, line)
		}
	}
	return parsedFiles, summary
}

// runGit extracts per-file git intelligence, treating "not a git repo" as a
// soft warning rather than a fatal error.
func (o *ingestOptions) runGit(ctx context.Context, out io.Writer, absRoot string) []git.PerFile {
	ix := &git.Indexer{MaxCommits: o.gitMaxCommits}
	recs, err := ix.Index(ctx, absRoot)
	if err != nil {
		fmt.Fprintf(out, "\ngit intelligence skipped: %v\n", err)
		return nil
	}
	printGitSummary(out, recs)
	return recs
}

// buildIngestGraph builds the dependency graph + metrics, then runs dead-code
// and health analysis over it, printing each summary.
func buildIngestGraph(out io.Writer, absRoot string, parsedFiles []models.ParsedFile, gitRecords []git.PerFile) (*graph.Graph, []deadcode.Finding, health.Result) {
	rctx := resolver.Context{}
	if modPath, err := gomod.ParseModulePath(absRoot); err == nil && modPath != "" {
		rctx.GoModulePath = modPath
	}
	if crate, err := cargo.ParseCrateName(absRoot); err == nil && crate != "" {
		rctx.RustCrateName = crate
	}
	b := graph.NewBuilder(nil, graph.Options{ResolverContext: rctx})
	for _, pf := range parsedFiles {
		b.AddFile(pf)
	}
	g := b.Build()
	g.ComputeMetrics()
	printGraphSummary(out, g)

	dcFindings := (&deadcode.Analyzer{MinConfidence: 0.5}).Analyze(g)
	printDeadCodeSummary(out, dcFindings)

	healthOpts := &health.Analyzer{}
	if len(gitRecords) > 0 {
		hat := make(map[string]struct{}, len(gitRecords))
		for _, gr := range gitRecords {
			if gr.IsHotspot {
				hat[gr.Path] = struct{}{}
			}
		}
		healthOpts.HotspotPaths = hat
	}
	healthResult := healthOpts.Analyze(parsedFiles)
	printHealthSummary(out, healthResult)
	return g, dcFindings, healthResult
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

// printExternalsSummary writes a per-ecosystem count plus a sample of
// resolved deps. Mirrors the graph summary's shape.
func printExternalsSummary(w io.Writer, records []external.Record) {
	fmt.Fprintf(w, "\nExternal systems: %d records\n", len(records))
	if len(records) == 0 {
		return
	}
	byEco := map[string]int{}
	devByEco := map[string]int{}
	for _, r := range records {
		byEco[r.Ecosystem]++
		if r.IsDevDep {
			devByEco[r.Ecosystem]++
		}
	}
	ecosystems := make([]string, 0, len(byEco))
	for e := range byEco {
		ecosystems = append(ecosystems, e)
	}
	sort.Strings(ecosystems)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	for _, e := range ecosystems {
		fmt.Fprintf(tw, "  %s\ttotal=%d\tdev=%d\n", e, byEco[e], devByEco[e])
	}
}

// persistAll opens the configured DB, runs pending migrations, ensures the
// repository row, and writes graph + externals + git intelligence + dead
// code findings (whichever inputs are non-nil).
func persistAll(ctx context.Context, repoArg, absRoot string, g *graph.Graph, exts []external.Record, gitRecs []git.PerFile, dcFindings []deadcode.Finding, healthResult health.Result) error {
	conn, dialect, err := openDB(ctx, repoArg)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := migrations.Up(ctx, conn, dialect); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	repoStore := repos.New(conn)
	repoRow, err := repoStore.EnsureByLocalPath(ctx, absRoot, "")
	if err != nil {
		return fmt.Errorf("ensure repository: %w", err)
	}

	// Best-effort: stamp the head_commit when git is available.
	if head, err := git.ResolveHeadCommit(absRoot); err == nil {
		_ = repoStore.UpdateHeadCommit(ctx, repoRow.ID, head.String())
	}

	if g != nil {
		if err := graphstore.New(conn).ReplaceGraph(ctx, repoRow.ID, g); err != nil {
			return fmt.Errorf("graph: %w", err)
		}
	}
	if err := externalstore.New(conn).ReplaceAll(ctx, repoRow.ID, exts); err != nil {
		return fmt.Errorf("externals: %w", err)
	}
	if err := gitstore.New(conn).ReplaceAll(ctx, repoRow.ID, gitRecs); err != nil {
		return fmt.Errorf("git_metadata: %w", err)
	}
	if err := deadstore.New(conn).ReplaceAll(ctx, repoRow.ID, dcFindings); err != nil {
		return fmt.Errorf("dead_code_findings: %w", err)
	}
	if err := healthstore.New(conn).ReplaceAll(ctx, repoRow.ID, healthResult); err != nil {
		return fmt.Errorf("health: %w", err)
	}
	return nil
}

// printHealthSummary writes a top-line health summary: average file
// score, finding counts per biomarker, and the worst 5 files by score.
func printHealthSummary(w io.Writer, r health.Result) {
	if len(r.FileMetrics) == 0 {
		return
	}
	total := 0.0
	for _, m := range r.FileMetrics {
		total += m.Score
	}
	avg := total / float64(len(r.FileMetrics))

	byKind := map[string]int{}
	for _, f := range r.Findings {
		byKind[f.BiomarkerType]++
	}

	fmt.Fprintf(w, "\nCode health: avg score %.2f / 10  (%d findings)\n", avg, len(r.Findings))
	if len(byKind) > 0 {
		kinds := make([]string, 0, len(byKind))
		for k := range byKind {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, k := range kinds {
			fmt.Fprintf(tw, "  %s\t%d\n", k, byKind[k])
		}
		tw.Flush()
	}

	// Worst-5 files by score.
	worst := append([]health.FileMetric(nil), r.FileMetrics...)
	sort.Slice(worst, func(i, j int) bool { return worst[i].Score < worst[j].Score })
	top := 5
	if len(worst) < top {
		top = len(worst)
	}
	if top > 0 {
		fmt.Fprintln(w, "Worst-scoring files:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for i := 0; i < top; i++ {
			fmt.Fprintf(tw, "  %.2f\t%s\t(maxCCN=%d)\n", worst[i].Score, worst[i].FilePath, worst[i].MaxCCN)
		}
		tw.Flush()
	}
}

// printDeadCodeSummary prints how many dead-code findings the analyzer
// produced, broken down by kind, plus the top-5 by confidence.
func printDeadCodeSummary(w io.Writer, findings []deadcode.Finding) {
	if len(findings) == 0 {
		fmt.Fprintln(w, "\nDead code: none found")
		return
	}
	files, syms := 0, 0
	for _, f := range findings {
		switch f.Kind {
		case deadcode.KindUnreachableFile:
			files++
		case deadcode.KindUnreachableSymbol:
			syms++
		}
	}
	fmt.Fprintf(w, "\nDead code: %d findings (%d files, %d symbols)\n", len(findings), files, syms)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	top := 5
	if len(findings) < top {
		top = len(findings)
	}
	for i := 0; i < top; i++ {
		f := findings[i]
		label := f.FilePath
		if f.SymbolName != "" {
			label = fmt.Sprintf("%s::%s", f.FilePath, f.SymbolName)
		}
		flag := ""
		if f.SafeToDelete {
			flag = " SAFE"
		}
		fmt.Fprintf(tw, "  %.2f%s\t%s\t%s\n", f.Confidence, flag, label, f.Reason)
	}
}

// printGitSummary prints a top-line overview of git intelligence: total
// files seen, hotspot count, and the top-5 hotspots by churn percentile.
func printGitSummary(w io.Writer, records []git.PerFile) {
	hotspots := 0
	for _, r := range records {
		if r.IsHotspot {
			hotspots++
		}
	}
	fmt.Fprintf(w, "\nGit intelligence: %d files (%d hotspots)\n", len(records), hotspots)
	if len(records) == 0 {
		return
	}
	// Sort hotspots by churn pctl desc and show up to 5.
	rec := append([]git.PerFile(nil), records...)
	sort.Slice(rec, func(i, j int) bool { return rec[i].ChurnPercentile > rec[j].ChurnPercentile })
	top := 5
	if len(rec) < top {
		top = len(rec)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	for i := 0; i < top; i++ {
		r := rec[i]
		owner := "(unknown)"
		if r.PrimaryOwner != nil {
			owner = r.PrimaryOwner.Name
		}
		hotspot := ""
		if r.IsHotspot {
			hotspot = " HOT"
		}
		fmt.Fprintf(tw, "  %.2f%s\t%s\towner=%s\t(commits=%d 90d=%d)\n",
			r.ChurnPercentile, hotspot, r.Path, owner, r.CommitCountTotal, r.CommitCount90d)
	}
}

func databaseTarget(repoArg string) string {
	abs, err := filepath.Abs(filepath.Join(repoArg, ".repowise", "wiki.db"))
	if err != nil {
		return repoArg + "/.repowise/wiki.db"
	}
	return abs
}
