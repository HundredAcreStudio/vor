package health_test

import (
	"testing"

	"github.com/HundredAcreStudio/vor/internal/analysis/health"
	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

// manySymbols builds n top-level function symbols in one file.
func manySymbols(n int) []models.Symbol {
	out := make([]models.Symbol, n)
	for i := range out {
		out[i] = models.Symbol{Name: "fn", Kind: models.KindFunction, StartLine: i + 1, EndLine: i + 1}
	}
	return out
}

func TestGodFile(t *testing.T) {
	a := &health.Analyzer{}
	res := a.Analyze([]models.ParsedFile{
		{FileInfo: models.FileInfo{Path: "small.go", Language: "go"}, Symbols: manySymbols(19)},
		{FileInfo: models.FileInfo{Path: "medium.go", Language: "go"}, Symbols: manySymbols(20)},
		{FileInfo: models.FileInfo{Path: "huge.go", Language: "go"}, Symbols: manySymbols(40)},
	})
	if n := countBiomarker(res, health.BiomarkerGodFile); n != 2 {
		t.Fatalf("want 2 god_file findings (medium.go, huge.go), got %d", n)
	}
	for _, f := range res.Findings {
		if f.BiomarkerType != health.BiomarkerGodFile {
			continue
		}
		switch f.FilePath {
		case "small.go":
			t.Error("small.go (19 symbols) should not be flagged")
		case "medium.go":
			if f.Severity != health.SeverityMedium {
				t.Errorf("medium.go: want medium, got %s", f.Severity)
			}
		case "huge.go":
			if f.Severity != health.SeverityHigh {
				t.Errorf("huge.go: want high, got %s", f.Severity)
			}
		}
	}
}

// god_file counts only top-level symbols, so a file built around one big class
// (40 methods, one struct) is left to god_class and never double-counted.
func TestGodFile_DoesNotDoubleCountGodClass(t *testing.T) {
	syms := []models.Symbol{{Name: "Big", Kind: models.KindStruct, StartLine: 1, EndLine: 200}}
	for i := range 40 {
		parent := "Big"
		syms = append(syms, models.Symbol{Name: "m", Kind: models.KindMethod, ParentName: &parent, StartLine: i + 2, EndLine: i + 2})
	}
	res := (&health.Analyzer{}).Analyze([]models.ParsedFile{{
		FileInfo: models.FileInfo{Path: "big.go", Language: "go"}, Symbols: syms,
	}})
	if n := countBiomarker(res, health.BiomarkerGodFile); n != 0 {
		t.Errorf("file with one struct + 40 methods should not be a god_file, got %d", n)
	}
	if n := countBiomarker(res, health.BiomarkerGodClass); n != 1 {
		t.Errorf("want the struct flagged as god_class instead, got %d", n)
	}
}

// Bare constants/variables don't count toward the top-level tally.
func TestGodFile_IgnoresConstantsAndVariables(t *testing.T) {
	syms := make([]models.Symbol, 30)
	for i := range syms {
		syms[i] = models.Symbol{Name: "C", Kind: models.KindConstant, StartLine: i + 1, EndLine: i + 1}
	}
	res := (&health.Analyzer{}).Analyze([]models.ParsedFile{{
		FileInfo: models.FileInfo{Path: "consts.go", Language: "go"}, Symbols: syms,
	}})
	if n := countBiomarker(res, health.BiomarkerGodFile); n != 0 {
		t.Errorf("a file of 30 constants should not be a god_file, got %d", n)
	}
}

// chain links a child to a parent in one file's heritage.
func heritageFile(path string, rels ...models.HeritageRelation) models.ParsedFile {
	return models.ParsedFile{FileInfo: models.FileInfo{Path: path, Language: "go"}, Heritage: rels}
}

func TestDeepInheritance(t *testing.T) {
	// Chain: D -> C -> B -> A -> Base  (D has 4 ancestors => DIT 4, medium).
	rel := func(child, parent string) models.HeritageRelation {
		return models.HeritageRelation{ChildName: child, ParentName: parent, Kind: models.HeritageExtends}
	}
	files := []models.ParsedFile{
		{
			FileInfo: models.FileInfo{Path: "tree.go", Language: "go"},
			Symbols: []models.Symbol{
				{Name: "D", Kind: models.KindClass, StartLine: 1, EndLine: 5},
				{Name: "C", Kind: models.KindClass, StartLine: 7, EndLine: 11},
			},
			Heritage: []models.HeritageRelation{rel("D", "C"), rel("C", "B"), rel("B", "A"), rel("A", "Base")},
		},
	}
	res := (&health.Analyzer{}).Analyze(files)
	flagged := map[string]health.Severity{}
	for _, f := range res.Findings {
		if f.BiomarkerType == health.BiomarkerDeepInheritance {
			flagged[f.FunctionName] = f.Severity
		}
	}
	// D=DIT4, C=DIT3, B=2, A=1. Only D reaches the medium threshold (4).
	if _, ok := flagged["D"]; !ok {
		t.Errorf("D (DIT 4) should be flagged, got %v", flagged)
	}
	if _, ok := flagged["C"]; ok {
		t.Errorf("C (DIT 3) should not be flagged")
	}
	if sev := flagged["D"]; sev != health.SeverityMedium {
		t.Errorf("D should be medium at DIT 4, got %s", sev)
	}
	// D is defined in tree.go — finding should carry the location.
	for _, f := range res.Findings {
		if f.BiomarkerType == health.BiomarkerDeepInheritance && f.FunctionName == "D" && f.FilePath != "tree.go" {
			t.Errorf("D's finding should point at tree.go, got %q", f.FilePath)
		}
	}
}

func TestDeepInheritance_HighSeverityAndCrossFile(t *testing.T) {
	rel := func(child, parent string) models.HeritageRelation {
		return models.HeritageRelation{ChildName: child, ParentName: parent, Kind: models.HeritageExtends}
	}
	// Chain spanning two files: leaf -> L6 -> L5 -> L4 -> L3 -> L2 -> L1
	// (6 edges => leaf DIT 6 => high).
	files := []models.ParsedFile{
		heritageFile("a.go", rel("leaf", "L6"), rel("L6", "L5"), rel("L5", "L4")),
		heritageFile("b.go", rel("L4", "L3"), rel("L3", "L2"), rel("L2", "L1")),
	}
	res := (&health.Analyzer{}).Analyze(files)
	var leaf *health.Finding
	for i := range res.Findings {
		if res.Findings[i].BiomarkerType == health.BiomarkerDeepInheritance && res.Findings[i].FunctionName == "leaf" {
			leaf = &res.Findings[i]
		}
	}
	if leaf == nil {
		t.Fatal("leaf (DIT 6 across files) should be flagged")
	}
	if leaf.Severity != health.SeverityHigh {
		t.Errorf("leaf at DIT 6 should be high, got %s", leaf.Severity)
	}
}

func TestDeepInheritance_NoInheritanceNoFindings(t *testing.T) {
	res := (&health.Analyzer{}).Analyze([]models.ParsedFile{{
		FileInfo: models.FileInfo{Path: "flat.go", Language: "go"},
		Symbols:  []models.Symbol{{Name: "Plain", Kind: models.KindStruct, StartLine: 1, EndLine: 3}},
	}})
	if n := countBiomarker(res, health.BiomarkerDeepInheritance); n != 0 {
		t.Errorf("no heritage relations => no deep_inheritance findings, got %d", n)
	}
}

// A cycle in the heritage data must not hang resolution.
func TestDeepInheritance_CycleTerminates(t *testing.T) {
	rel := func(child, parent string) models.HeritageRelation {
		return models.HeritageRelation{ChildName: child, ParentName: parent, Kind: models.HeritageExtends}
	}
	files := []models.ParsedFile{heritageFile("c.go", rel("X", "Y"), rel("Y", "Z"), rel("Z", "X"))}
	// Should return without deadlock; cycle depth stays bounded under threshold.
	_ = (&health.Analyzer{}).Analyze(files)
}
