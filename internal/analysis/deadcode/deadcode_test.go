package deadcode_test

import (
	"slices"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/deadcode"
	"github.com/repowise-dev/repowise-go/internal/ingestion/graph"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// buildGraph composes a small reproducible graph: an entry-point file
// imports a lib file; lib exposes a function called from main; a third
// "orphan" file isn't imported by anyone and defines two symbols, only
// one of which is locally called.
func buildGraph() *graph.Graph {
	g := graph.New()

	mainFile := g.AddFileNode(models.FileInfo{Path: "main.go", Language: "go", IsEntryPoint: true})
	libFile := g.AddFileNode(models.FileInfo{Path: "lib.go", Language: "go"})
	orphan := g.AddFileNode(models.FileInfo{Path: "orphan.go", Language: "go"})

	mainSym := g.AddSymbolNode("main.go", models.Symbol{
		ID: "main.go::main", Name: "main", Kind: models.KindFunction,
		Visibility: models.VisibilityPublic, Language: "go", StartLine: 1, EndLine: 10,
	})
	helper := g.AddSymbolNode("lib.go", models.Symbol{
		ID: "lib.go::Helper", Name: "Helper", Kind: models.KindFunction,
		Visibility: models.VisibilityPublic, Language: "go", StartLine: 1, EndLine: 5,
	})
	orphanA := g.AddSymbolNode("orphan.go", models.Symbol{
		ID: "orphan.go::Used", Name: "Used", Kind: models.KindFunction,
		Visibility: models.VisibilityPublic, Language: "go", StartLine: 1, EndLine: 5,
	})
	orphanB := g.AddSymbolNode("orphan.go", models.Symbol{
		ID: "orphan.go::Unused", Name: "Unused", Kind: models.KindFunction,
		Visibility: models.VisibilityPublic, Language: "go", StartLine: 10, EndLine: 15,
	})

	g.AddEdge(mainFile, mainSym, models.EdgeDefines, 1.0, nil)
	g.AddEdge(libFile, helper, models.EdgeDefines, 1.0, nil)
	g.AddEdge(orphan, orphanA, models.EdgeDefines, 1.0, nil)
	g.AddEdge(orphan, orphanB, models.EdgeDefines, 1.0, nil)

	g.AddEdge(mainFile, libFile, models.EdgeImports, 1.0, nil)
	g.AddEdge(mainSym, helper, models.EdgeCalls, 1.0, nil)

	// orphanA is referenced by orphanB but neither is reachable from main.
	g.AddEdge(orphanB, orphanA, models.EdgeCalls, 1.0, nil)

	return g
}

func TestAnalyze_FlagsUnreachableFileAndSymbols(t *testing.T) {
	g := buildGraph()
	a := &deadcode.Analyzer{}
	findings := a.Analyze(g)

	// Expect orphan.go (file) and both orphan symbols to be dead.
	wantFiles := []string{"orphan.go"}
	wantSymbols := []string{"Used", "Unused"}

	var gotFile bool
	gotSyms := map[string]deadcode.Finding{}
	for _, f := range findings {
		if f.Kind == deadcode.KindUnreachableFile && f.FilePath == "orphan.go" {
			gotFile = true
			if !f.SafeToDelete {
				t.Errorf("orphan.go should be SafeToDelete (no incoming imports)")
			}
		}
		if f.Kind == deadcode.KindUnreachableSymbol && f.FilePath == "orphan.go" {
			gotSyms[f.SymbolName] = f
		}
	}
	if !gotFile {
		t.Errorf("orphan.go missing from findings: %+v", findings)
	}
	for _, want := range wantSymbols {
		if _, ok := gotSyms[want]; !ok {
			t.Errorf("symbol %q missing from findings: %v", want, gotSyms)
		}
	}
	// orphanA has an incoming call from orphanB (also dead) → lower confidence.
	if gotSyms["Used"].Confidence >= gotSyms["Unused"].Confidence {
		t.Errorf("Used should have lower confidence than Unused: %v vs %v",
			gotSyms["Used"].Confidence, gotSyms["Unused"].Confidence)
	}

	_ = wantFiles
}

func TestAnalyze_LiveCodeNotFlagged(t *testing.T) {
	g := buildGraph()
	findings := (&deadcode.Analyzer{}).Analyze(g)

	for _, f := range findings {
		if f.FilePath == "main.go" {
			t.Errorf("entry-point main.go was flagged dead: %+v", f)
		}
		if f.FilePath == "lib.go" {
			t.Errorf("imported lib.go was flagged dead: %+v", f)
		}
		if f.SymbolName == "Helper" {
			t.Errorf("called Helper was flagged dead: %+v", f)
		}
	}
}

func TestAnalyze_ExtraRootsKeepNodesLive(t *testing.T) {
	g := buildGraph()
	// Mark orphanA as a runtime-registered handler → live.
	a := &deadcode.Analyzer{ExtraRoots: []string{"orphan.go::Used"}}
	findings := a.Analyze(g)

	for _, f := range findings {
		if f.SymbolName == "Used" {
			t.Errorf("Used was flagged dead despite being an ExtraRoot: %+v", f)
		}
	}
	// orphan.go (file) should still be flagged — file is dead even though
	// one of its symbols is now live, because nothing imports the file.
	gotFile := false
	for _, f := range findings {
		if f.Kind == deadcode.KindUnreachableFile && f.FilePath == "orphan.go" {
			gotFile = true
		}
	}
	if !gotFile {
		t.Errorf("orphan.go file should still appear (file has no inbound import edges)")
	}
}

func TestAnalyze_MinConfidenceFilters(t *testing.T) {
	g := buildGraph()
	all := (&deadcode.Analyzer{}).Analyze(g)
	highOnly := (&deadcode.Analyzer{MinConfidence: 0.9}).Analyze(g)
	if len(highOnly) >= len(all) {
		t.Errorf("MinConfidence=0.9 should reduce findings: %d vs %d", len(highOnly), len(all))
	}
	for _, f := range highOnly {
		if f.Confidence < 0.9 {
			t.Errorf("finding below 0.9 leaked through: %+v", f)
		}
	}
}

func TestAnalyze_IncludeTestFilesFlag(t *testing.T) {
	g := graph.New()
	main := g.AddFileNode(models.FileInfo{Path: "main.go", Language: "go", IsEntryPoint: true})
	tst := g.AddFileNode(models.FileInfo{Path: "foo_test.go", Language: "go", IsTest: true})
	tstSym := g.AddSymbolNode("foo_test.go", models.Symbol{
		ID: "foo_test.go::TestFoo", Name: "TestFoo", Kind: models.KindFunction,
		Visibility: models.VisibilityPublic, Language: "go", StartLine: 1, EndLine: 5,
	})
	g.AddEdge(tst, tstSym, models.EdgeDefines, 1.0, nil)
	_ = main

	default_ := (&deadcode.Analyzer{}).Analyze(g)
	withTests := (&deadcode.Analyzer{IncludeTestFiles: true}).Analyze(g)

	if hasPath(default_, "foo_test.go") {
		t.Errorf("test file should be excluded by default; got %+v", default_)
	}
	if !hasPath(withTests, "foo_test.go") {
		t.Errorf("test file should appear when IncludeTestFiles=true")
	}
}

func TestAnalyze_SortedByConfidenceDesc(t *testing.T) {
	g := buildGraph()
	findings := (&deadcode.Analyzer{}).Analyze(g)
	for i := 1; i < len(findings); i++ {
		if findings[i].Confidence > findings[i-1].Confidence {
			t.Errorf("findings not sorted desc at index %d: %+v", i, findings)
			break
		}
	}
}

func hasPath(findings []deadcode.Finding, path string) bool {
	return slices.ContainsFunc(findings, func(f deadcode.Finding) bool { return f.FilePath == path })
}
