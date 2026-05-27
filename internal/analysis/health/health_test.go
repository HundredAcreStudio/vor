package health_test

import (
	"slices"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/health"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

func mkSym(name string, ccn, startLine, endLine int) models.Symbol {
	return models.Symbol{
		ID:                 "f.go::" + name,
		Name:               name,
		Kind:               models.KindFunction,
		ComplexityEstimate: ccn,
		StartLine:          startLine,
		EndLine:            endLine,
		Visibility:         models.VisibilityPublic,
		Language:           "go",
	}
}

func mkFile(symbols []models.Symbol) models.ParsedFile {
	return models.ParsedFile{
		FileInfo: models.FileInfo{Path: "f.go", Language: "go"},
		Symbols:  symbols,
	}
}

func TestAnalyze_HighComplexitySeverity(t *testing.T) {
	a := &health.Analyzer{}
	res := a.Analyze([]models.ParsedFile{
		mkFile([]models.Symbol{
			mkSym("trivial", 3, 1, 5),
			mkSym("warn", 12, 10, 20),
			mkSym("high", 25, 30, 40),
			mkSym("critical", 60, 50, 60),
		}),
	})

	// trivial should not produce a finding.
	for _, f := range res.Findings {
		if f.FunctionName == "trivial" {
			t.Errorf("trivial should not produce a finding: %+v", f)
		}
	}

	byFn := map[string]health.Finding{}
	for _, f := range res.Findings {
		if f.BiomarkerType == health.BiomarkerHighComplexity {
			byFn[f.FunctionName] = f
		}
	}
	if byFn["warn"].Severity != health.SeverityMedium {
		t.Errorf("warn severity = %s, want medium", byFn["warn"].Severity)
	}
	if byFn["high"].Severity != health.SeverityHigh {
		t.Errorf("high severity = %s, want high", byFn["high"].Severity)
	}
	if byFn["critical"].Severity != health.SeverityCritical {
		t.Errorf("critical severity = %s, want critical", byFn["critical"].Severity)
	}
}

func TestAnalyze_LongFunctionSeverity(t *testing.T) {
	a := &health.Analyzer{}
	res := a.Analyze([]models.ParsedFile{
		mkFile([]models.Symbol{
			mkSym("short", 2, 1, 30),
			mkSym("medium", 2, 100, 170),   // 71 lines
			mkSym("verylong", 2, 200, 350), // 151 lines
		}),
	})
	byFn := map[string]health.Finding{}
	for _, f := range res.Findings {
		if f.BiomarkerType == health.BiomarkerLongFunction {
			byFn[f.FunctionName] = f
		}
	}
	if _, ok := byFn["short"]; ok {
		t.Errorf("short shouldn't be flagged")
	}
	if byFn["medium"].Severity != health.SeverityMedium {
		t.Errorf("medium severity = %s, want medium", byFn["medium"].Severity)
	}
	if byFn["verylong"].Severity != health.SeverityHigh {
		t.Errorf("verylong severity = %s, want high", byFn["verylong"].Severity)
	}
}

func TestAnalyze_FileMetricMaxCCN(t *testing.T) {
	a := &health.Analyzer{}
	res := a.Analyze([]models.ParsedFile{
		mkFile([]models.Symbol{
			mkSym("a", 2, 1, 5),
			mkSym("b", 15, 10, 20),
			mkSym("c", 7, 30, 40),
		}),
	})
	if len(res.FileMetrics) != 1 {
		t.Fatalf("FileMetrics = %d, want 1", len(res.FileMetrics))
	}
	if res.FileMetrics[0].MaxCCN != 15 {
		t.Errorf("MaxCCN = %d, want 15", res.FileMetrics[0].MaxCCN)
	}
}

func TestAnalyze_FileScoreReducesWithFindings(t *testing.T) {
	a := &health.Analyzer{}
	clean := a.Analyze([]models.ParsedFile{
		mkFile([]models.Symbol{mkSym("ok", 3, 1, 10)}),
	})
	if clean.FileMetrics[0].Score != 10.0 {
		t.Errorf("clean file score = %v, want 10.0", clean.FileMetrics[0].Score)
	}

	bad := a.Analyze([]models.ParsedFile{
		mkFile([]models.Symbol{mkSym("nope", 60, 1, 200)}),
	})
	if bad.FileMetrics[0].Score >= clean.FileMetrics[0].Score {
		t.Errorf("bad file should have lower score: %v vs %v",
			bad.FileMetrics[0].Score, clean.FileMetrics[0].Score)
	}
	if bad.FileMetrics[0].Score < 1.0 {
		t.Errorf("file score = %v, should be clamped to >= 1.0", bad.FileMetrics[0].Score)
	}
}

func TestAnalyze_CustomThresholds(t *testing.T) {
	a := &health.Analyzer{Thresholds: health.Thresholds{
		ComplexityWarning:     5,
		ComplexityHigh:        8,
		ComplexityCritical:    15,
		LongFunctionLines:     20,
		VeryLongFunctionLines: 40,
	}}
	res := a.Analyze([]models.ParsedFile{
		mkFile([]models.Symbol{
			mkSym("low", 4, 1, 5),  // below new warning
			mkSym("hit", 5, 10, 15), // hits new warning
		}),
	})
	flagged := false
	for _, f := range res.Findings {
		if f.FunctionName == "hit" {
			flagged = true
		}
		if f.FunctionName == "low" {
			t.Errorf("low should not be flagged with custom threshold")
		}
	}
	if !flagged {
		t.Errorf("hit should be flagged with custom warning threshold")
	}
}

func TestAnalyze_BiomarkerKindsKnown(t *testing.T) {
	a := &health.Analyzer{}
	res := a.Analyze([]models.ParsedFile{
		mkFile([]models.Symbol{mkSym("bad", 25, 1, 100)}),
	})
	kinds := []string{}
	for _, f := range res.Findings {
		kinds = append(kinds, f.BiomarkerType)
	}
	if !slices.Contains(kinds, health.BiomarkerHighComplexity) {
		t.Errorf("missing high_complexity finding")
	}
	if !slices.Contains(kinds, health.BiomarkerLongFunction) {
		t.Errorf("missing long_function finding")
	}
}

func TestAnalyze_DeepNestingSeverity(t *testing.T) {
	mkSymWithNesting := func(name string, depth int) models.Symbol {
		s := mkSym(name, 1, 1, 5)
		s.NestingDepth = depth
		return s
	}
	a := &health.Analyzer{}
	res := a.Analyze([]models.ParsedFile{
		mkFile([]models.Symbol{
			mkSymWithNesting("flat", 1),
			mkSymWithNesting("warn", 4),
			mkSymWithNesting("deep", 7),
		}),
	})
	byFn := map[string]health.Finding{}
	for _, f := range res.Findings {
		if f.BiomarkerType == health.BiomarkerDeepNesting {
			byFn[f.FunctionName] = f
		}
	}
	if _, ok := byFn["flat"]; ok {
		t.Errorf("flat shouldn't be flagged")
	}
	if byFn["warn"].Severity != health.SeverityMedium {
		t.Errorf("warn severity = %s, want medium", byFn["warn"].Severity)
	}
	if byFn["deep"].Severity != health.SeverityHigh {
		t.Errorf("deep severity = %s, want high", byFn["deep"].Severity)
	}
}

func TestAnalyze_FileMaxNesting(t *testing.T) {
	mkSymWithNesting := func(name string, depth int) models.Symbol {
		s := mkSym(name, 1, 1, 5)
		s.NestingDepth = depth
		return s
	}
	res := (&health.Analyzer{}).Analyze([]models.ParsedFile{
		mkFile([]models.Symbol{
			mkSymWithNesting("a", 1),
			mkSymWithNesting("b", 5),
			mkSymWithNesting("c", 3),
		}),
	})
	if res.FileMetrics[0].MaxNesting != 5 {
		t.Errorf("MaxNesting = %d, want 5", res.FileMetrics[0].MaxNesting)
	}
}

func TestAnalyze_GodClass(t *testing.T) {
	parent := "BigClass"
	syms := []models.Symbol{
		{ID: "f.go::BigClass", Name: "BigClass", Kind: models.KindClass,
			Language: "go", StartLine: 1, EndLine: 200, Visibility: models.VisibilityPublic},
	}
	// 17 methods → above default 15 threshold (medium), below 25 (high).
	for i := 0; i < 17; i++ {
		syms = append(syms, models.Symbol{
			ID: "f.go::BigClass::m" + string(rune('a'+i)),
			Name: "m" + string(rune('a'+i)),
			Kind: models.KindMethod, ParentName: &parent,
			Language: "go", StartLine: i * 5, EndLine: i*5 + 4,
		})
	}
	res := (&health.Analyzer{}).Analyze([]models.ParsedFile{mkFile(syms)})
	var found *health.Finding
	for i := range res.Findings {
		if res.Findings[i].BiomarkerType == health.BiomarkerGodClass {
			found = &res.Findings[i]
		}
	}
	if found == nil {
		t.Fatalf("god_class finding missing: %+v", res.Findings)
	}
	if found.FunctionName != "BigClass" {
		t.Errorf("FunctionName = %q, want BigClass", found.FunctionName)
	}
	if found.Severity != health.SeverityMedium {
		t.Errorf("Severity = %s, want medium", found.Severity)
	}
}

func TestAnalyze_GodClass_HighSeverity(t *testing.T) {
	parent := "Huge"
	syms := []models.Symbol{
		{ID: "f.go::Huge", Name: "Huge", Kind: models.KindClass,
			Language: "go", StartLine: 1, EndLine: 500, Visibility: models.VisibilityPublic},
	}
	// 30 methods → above 25 high threshold.
	for i := 0; i < 30; i++ {
		syms = append(syms, models.Symbol{
			ID:         "f.go::Huge::m" + string(rune('a'+i)),
			Name:       "m" + string(rune('a'+i)),
			Kind:       models.KindMethod, ParentName: &parent,
			Language:   "go",
		})
	}
	res := (&health.Analyzer{}).Analyze([]models.ParsedFile{mkFile(syms)})
	for _, f := range res.Findings {
		if f.BiomarkerType == health.BiomarkerGodClass {
			if f.Severity != health.SeverityHigh {
				t.Errorf("Severity = %s, want high", f.Severity)
			}
		}
	}
}

func TestAnalyze_GodClass_BelowThreshold(t *testing.T) {
	parent := "Small"
	syms := []models.Symbol{
		{ID: "f.go::Small", Name: "Small", Kind: models.KindClass,
			Language: "go", Visibility: models.VisibilityPublic},
	}
	for i := 0; i < 5; i++ {
		syms = append(syms, models.Symbol{
			ID: "f.go::Small::m", Name: "m" + string(rune('a'+i)),
			Kind: models.KindMethod, ParentName: &parent, Language: "go",
		})
	}
	res := (&health.Analyzer{}).Analyze([]models.ParsedFile{mkFile(syms)})
	for _, f := range res.Findings {
		if f.BiomarkerType == health.BiomarkerGodClass {
			t.Errorf("Small class (5 methods) shouldn't be flagged: %+v", f)
		}
	}
}

// TestAnalyze_UntestedHotspot covers the cross-cutting biomarker that
// combines git intelligence (HotspotPaths) with the parser output (test
// file detection). Three files in the fixture:
//
//	pkg/foo.go      — hotspot, NO test pair  → flagged
//	pkg/bar.go      — hotspot, paired with bar_test.go → NOT flagged
//	pkg/baz.go      — NOT a hotspot, no test pair → NOT flagged
//
// A fourth file (pkg/bar_test.go) is the paired test that suppresses the
// bar.go finding.
func TestAnalyze_UntestedHotspot(t *testing.T) {
	files := []models.ParsedFile{
		{FileInfo: models.FileInfo{Path: "pkg/foo.go", Language: "go"}},
		{FileInfo: models.FileInfo{Path: "pkg/bar.go", Language: "go"}},
		{FileInfo: models.FileInfo{Path: "pkg/baz.go", Language: "go"}},
		{FileInfo: models.FileInfo{Path: "pkg/bar_test.go", Language: "go", IsTest: true}},
	}
	a := &health.Analyzer{
		HotspotPaths: map[string]struct{}{
			"pkg/foo.go": {},
			"pkg/bar.go": {},
		},
	}
	res := a.Analyze(files)

	got := map[string]bool{}
	for _, f := range res.Findings {
		if f.BiomarkerType == health.BiomarkerUntestedHotspot {
			got[f.FilePath] = true
		}
	}
	if !got["pkg/foo.go"] {
		t.Errorf("pkg/foo.go should be flagged (hotspot, no test)")
	}
	if got["pkg/bar.go"] {
		t.Errorf("pkg/bar.go has bar_test.go — should NOT be flagged")
	}
	if got["pkg/baz.go"] {
		t.Errorf("pkg/baz.go is not a hotspot — should NOT be flagged")
	}

	// HasTestFile on metrics reflects the pairing detection.
	for _, m := range res.FileMetrics {
		switch m.FilePath {
		case "pkg/bar.go":
			if !m.HasTestFile {
				t.Errorf("pkg/bar.go HasTestFile = false, want true")
			}
		case "pkg/foo.go":
			if m.HasTestFile {
				t.Errorf("pkg/foo.go HasTestFile = true, want false")
			}
		}
	}
}

func TestAnalyze_UntestedHotspot_NoHotspotPathsSkipsBiomarker(t *testing.T) {
	files := []models.ParsedFile{
		{FileInfo: models.FileInfo{Path: "pkg/foo.go", Language: "go"}},
	}
	res := (&health.Analyzer{}).Analyze(files)
	for _, f := range res.Findings {
		if f.BiomarkerType == health.BiomarkerUntestedHotspot {
			t.Errorf("untested_hotspot fired with empty HotspotPaths: %+v", f)
		}
	}
}

func TestAnalyze_TestPairingPatterns(t *testing.T) {
	// Each row exercises one of the recognised naming conventions.
	cases := []struct {
		prod, test string
	}{
		{"pkg/foo.go", "pkg/foo_test.go"},     // Go
		{"app/util.py", "app/test_util.py"},   // Python prefix
		{"app/util.py", "app/util_test.py"},   // Python suffix
		{"web/calc.ts", "web/calc.test.ts"},   // TS .test
		{"web/calc.ts", "web/calc.spec.ts"},   // TS .spec
		{"src/Foo.java", "src/FooTest.java"},  // Java
	}
	for _, tc := range cases {
		files := []models.ParsedFile{
			{FileInfo: models.FileInfo{Path: tc.prod}},
			{FileInfo: models.FileInfo{Path: tc.test, IsTest: true}},
		}
		a := &health.Analyzer{HotspotPaths: map[string]struct{}{tc.prod: {}}}
		res := a.Analyze(files)
		flagged := false
		for _, f := range res.Findings {
			if f.BiomarkerType == health.BiomarkerUntestedHotspot && f.FilePath == tc.prod {
				flagged = true
			}
		}
		if flagged {
			t.Errorf("%s ↔ %s pairing not recognised; biomarker fired", tc.prod, tc.test)
		}
	}
}

// TestAnalyze_BrainMethod exercises the composite biomarker: a function
// must be high on all three axes simultaneously (CCN, nesting, lines).
// Each "bad on one axis only" case asserts the biomarker does NOT fire.
func TestAnalyze_BrainMethod(t *testing.T) {
	mkSymFull := func(name string, ccn, nesting, startLine, endLine int) models.Symbol {
		s := mkSym(name, ccn, startLine, endLine)
		s.NestingDepth = nesting
		return s
	}

	// Default thresholds: CCN≥10, nesting≥3, lines≥50.
	syms := []models.Symbol{
		mkSymFull("brain", 15, 4, 1, 80),         // bad on all 3 → flagged
		mkSymFull("just_complex", 15, 1, 100, 110), // CCN only
		mkSymFull("just_nested", 2, 5, 200, 210),   // nesting only
		mkSymFull("just_long", 2, 1, 300, 400),     // lines only
		mkSymFull("clean", 3, 1, 500, 510),         // nothing
	}
	res := (&health.Analyzer{}).Analyze([]models.ParsedFile{mkFile(syms)})

	brainHits := map[string]bool{}
	for _, f := range res.Findings {
		if f.BiomarkerType == health.BiomarkerBrainMethod {
			brainHits[f.FunctionName] = true
		}
	}
	if !brainHits["brain"] {
		t.Errorf("'brain' should fire brain_method (bad on all 3 axes)")
	}
	for _, name := range []string{"just_complex", "just_nested", "just_long", "clean"} {
		if brainHits[name] {
			t.Errorf("%s shouldn't fire brain_method (only one axis bad): %v", name, brainHits)
		}
	}
}

func TestAnalyze_BrainMethod_DisabledViaZeroThresholds(t *testing.T) {
	syms := []models.Symbol{
		{Name: "verybad", ComplexityEstimate: 100, NestingDepth: 10,
			StartLine: 1, EndLine: 500, Language: "go"},
	}
	a := &health.Analyzer{Thresholds: health.Thresholds{
		ComplexityWarning:  5, // need to set something so default fallback doesn't kick in
		ComplexityHigh:     10,
		ComplexityCritical: 100,
		// BrainMethod thresholds left at 0 → skip
	}}
	res := a.Analyze([]models.ParsedFile{mkFile(syms)})
	for _, f := range res.Findings {
		if f.BiomarkerType == health.BiomarkerBrainMethod {
			t.Errorf("brain_method shouldn't fire with zero thresholds: %+v", f)
		}
	}
}
