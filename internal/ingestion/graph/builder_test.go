package graph

import (
	"slices"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"

	// Side-effect imports: each per-language resolver registers itself
	// with the resolver registry, the same way the CLI / pipeline pulls
	// them in. Without these, builder tests get zero import edges
	// because Lookup() returns nil for every language.
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/golang"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/python"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/typescript"
)

// mkFile is a test helper that creates a ParsedFile with the given path,
// language, symbols, imports, and calls.
func mkFile(path string, lang models.LanguageTag, syms []models.Symbol, imps []models.Import, calls []models.CallSite) models.ParsedFile {
	return models.ParsedFile{
		FileInfo: models.FileInfo{Path: path, Language: lang},
		Symbols:  syms,
		Imports:  imps,
		Calls:    calls,
	}
}

// mkSym is a test helper.
func mkSym(id, name string, kind models.SymbolKind, parent *string, startLine, endLine int) models.Symbol {
	return models.Symbol{
		ID:         id,
		Name:       name,
		Kind:       kind,
		ParentName: parent,
		StartLine:  startLine,
		EndLine:    endLine,
		Visibility: models.VisibilityPublic,
		Language:   "go",
	}
}

func TestBuilder_DefinesAndHasMethodEdges(t *testing.T) {
	parent := "User"
	parsed := mkFile("pkg/user.go", "go",
		[]models.Symbol{
			mkSym("pkg/user.go::User", "User", models.KindClass, nil, 1, 20),
			mkSym("pkg/user.go::User::Save", "Save", models.KindMethod, &parent, 10, 15),
			mkSym("pkg/user.go::User::name", "name", models.KindVariable, &parent, 5, 5),
		}, nil, nil)

	b := NewBuilder(nil, Options{})
	b.AddFile(parsed)
	b.Build()

	g := b.g
	if g.NodeCount() != 4 {
		t.Errorf("NodeCount = %d, want 4 (1 file + 3 symbols)", g.NodeCount())
	}

	counts := g.CountByEdgeType()
	if counts[models.EdgeDefines] != 3 {
		t.Errorf("defines edges = %d, want 3", counts[models.EdgeDefines])
	}
	if counts[models.EdgeHasMethod] != 1 {
		t.Errorf("has_method edges = %d, want 1 (User -> Save)", counts[models.EdgeHasMethod])
	}
	if counts[models.EdgeHasProperty] != 1 {
		t.Errorf("has_property edges = %d, want 1 (User -> name)", counts[models.EdgeHasProperty])
	}
}

func TestBuilder_RelativeImportResolution(t *testing.T) {
	// web/src/index.ts imports "./calc" -> web/src/calc.ts
	indexParsed := mkFile("web/src/index.ts", "typescript", nil,
		[]models.Import{{ModulePath: "./calc"}}, nil)
	calcParsed := mkFile("web/src/calc.ts", "typescript",
		[]models.Symbol{mkSym("web/src/calc.ts::add", "add", models.KindFunction, nil, 1, 2)},
		nil, nil)

	b := NewBuilder(nil, Options{})
	b.AddFile(indexParsed)
	b.AddFile(calcParsed)
	b.Build()

	g := b.g
	counts := g.CountByEdgeType()
	if counts[models.EdgeImports] != 1 {
		t.Errorf("imports edges = %d, want 1", counts[models.EdgeImports])
	}
	out := g.Outgoing("web/src/index.ts")
	if len(out) != 1 || out[0].T.StringID != "web/src/calc.ts" {
		t.Errorf("index -> calc edge missing: outgoing = %v", out)
	}
}

func TestBuilder_PythonDottedImportResolution(t *testing.T) {
	// app/main.py: from app.utils import helper  ->  app/utils.py
	mainParsed := mkFile("app/main.py", "python", nil,
		[]models.Import{{ModulePath: "app.utils"}}, nil)
	utilsParsed := mkFile("app/utils.py", "python",
		[]models.Symbol{mkSym("app/utils.py::helper", "helper", models.KindFunction, nil, 1, 2)},
		nil, nil)

	b := NewBuilder(nil, Options{})
	b.AddFile(mainParsed)
	b.AddFile(utilsParsed)
	b.Build()

	if got := b.g.CountByEdgeType()[models.EdgeImports]; got != 1 {
		t.Errorf("imports edges = %d, want 1 (app.utils -> app/utils.py)", got)
	}
}

func TestBuilder_PythonInitImportResolution(t *testing.T) {
	// from app.subpkg import X  ->  app/subpkg/__init__.py (when no
	// app/subpkg.py file exists).
	mainParsed := mkFile("main.py", "python", nil,
		[]models.Import{{ModulePath: "app.subpkg"}}, nil)
	initParsed := mkFile("app/subpkg/__init__.py", "python", nil, nil, nil)

	b := NewBuilder(nil, Options{})
	b.AddFile(mainParsed)
	b.AddFile(initParsed)
	b.Build()

	if got := b.g.CountByEdgeType()[models.EdgeImports]; got != 1 {
		t.Errorf("imports edges = %d, want 1 via __init__.py", got)
	}
}

func TestBuilder_UnresolvableImportDroppedSilently(t *testing.T) {
	// External dep "react" with no matching file in the graph — no edge.
	parsed := mkFile("web/src/index.ts", "typescript", nil,
		[]models.Import{{ModulePath: "react"}}, nil)

	b := NewBuilder(nil, Options{})
	b.AddFile(parsed)
	b.Build()

	if got := b.g.CountByEdgeType()[models.EdgeImports]; got != 0 {
		t.Errorf("imports edges = %d, want 0 (react is external)", got)
	}
}

func TestBuilder_CallTier1_SameFile(t *testing.T) {
	callerID := "demo.go::Greet"
	calls := []models.CallSite{{
		TargetName:     "helper",
		Line:           5,
		CallerSymbolID: &callerID,
	}}
	parsed := mkFile("demo.go", "go",
		[]models.Symbol{
			mkSym("demo.go::Greet", "Greet", models.KindFunction, nil, 4, 6),
			mkSym("demo.go::helper", "helper", models.KindFunction, nil, 1, 2),
		}, nil, calls)

	b := NewBuilder(nil, Options{})
	b.AddFile(parsed)
	b.Build()

	out := b.g.Outgoing("demo.go::Greet")
	hasCall := slices.ContainsFunc(out, func(e *Edge) bool {
		return e.Type == models.EdgeCalls && e.T.StringID == "demo.go::helper"
	})
	if !hasCall {
		t.Errorf("Tier 1 call Greet->helper missing; outgoing = %v", outNames(out))
	}
	// Confidence on the resolved Tier 1 edge should be 1.0.
	for _, e := range out {
		if e.Type == models.EdgeCalls && e.Confidence != 1.0 {
			t.Errorf("Tier 1 call confidence = %v, want 1.0", e.Confidence)
		}
	}
}

func TestBuilder_CallTier2_ViaImport(t *testing.T) {
	// main.go imports "./calc"; main.Run() calls Add() from calc.
	callerID := "main.go::Run"
	rcv := "calc"
	calls := []models.CallSite{{
		TargetName:     "Add",
		ReceiverName:   &rcv,
		Line:           3,
		CallerSymbolID: &callerID,
	}}
	mainP := mkFile("main.go", "go",
		[]models.Symbol{mkSym("main.go::Run", "Run", models.KindFunction, nil, 2, 4)},
		[]models.Import{{ModulePath: "./calc"}}, calls)
	calcP := mkFile("calc.go", "go",
		[]models.Symbol{mkSym("calc.go::Add", "Add", models.KindFunction, nil, 1, 2)},
		nil, nil)

	b := NewBuilder(nil, Options{})
	b.AddFile(mainP)
	b.AddFile(calcP)
	b.Build()

	out := b.g.Outgoing("main.go::Run")
	hit := false
	for _, e := range out {
		if e.Type == models.EdgeCalls && e.T.StringID == "calc.go::Add" {
			hit = true
			if e.Confidence != 0.7 {
				t.Errorf("Tier 2 call confidence = %v, want 0.7", e.Confidence)
			}
		}
	}
	if !hit {
		t.Errorf("Tier 2 call Run->calc.Add missing; outgoing = %v", outNames(out))
	}
}

func TestBuilder_CallTier3_NameMatchAnywhere(t *testing.T) {
	// caller in main.go calls "Helper" which lives in unrelated.go
	// (no import edge between them — pure name match).
	callerID := "main.go::Run"
	calls := []models.CallSite{{
		TargetName:     "Helper",
		Line:           3,
		CallerSymbolID: &callerID,
	}}
	mainP := mkFile("main.go", "go",
		[]models.Symbol{mkSym("main.go::Run", "Run", models.KindFunction, nil, 2, 4)},
		nil, calls)
	otherP := mkFile("unrelated.go", "go",
		[]models.Symbol{mkSym("unrelated.go::Helper", "Helper", models.KindFunction, nil, 1, 2)},
		nil, nil)

	b := NewBuilder(nil, Options{})
	b.AddFile(mainP)
	b.AddFile(otherP)
	b.Build()

	out := b.g.Outgoing("main.go::Run")
	for _, e := range out {
		if e.Type == models.EdgeCalls && e.T.StringID == "unrelated.go::Helper" {
			if e.Confidence != 0.3 {
				t.Errorf("Tier 3 call confidence = %v, want 0.3", e.Confidence)
			}
			return
		}
	}
	t.Errorf("Tier 3 call Run->Helper missing; outgoing = %v", outNames(out))
}

func TestBuilder_MinCallConfidenceFiltersTier3(t *testing.T) {
	callerID := "main.go::Run"
	calls := []models.CallSite{{
		TargetName:     "Helper",
		Line:           3,
		CallerSymbolID: &callerID,
	}}
	mainP := mkFile("main.go", "go",
		[]models.Symbol{mkSym("main.go::Run", "Run", models.KindFunction, nil, 2, 4)},
		nil, calls)
	otherP := mkFile("unrelated.go", "go",
		[]models.Symbol{mkSym("unrelated.go::Helper", "Helper", models.KindFunction, nil, 1, 2)},
		nil, nil)

	b := NewBuilder(nil, Options{MinCallConfidence: 0.5})
	b.AddFile(mainP)
	b.AddFile(otherP)
	b.Build()

	if got := b.g.CountByEdgeType()[models.EdgeCalls]; got != 0 {
		t.Errorf("calls edges = %d, want 0 (Tier 3 below threshold)", got)
	}
}

func outNames(edges []*Edge) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		out = append(out, string(e.Type)+":"+e.T.StringID)
	}
	return out
}
