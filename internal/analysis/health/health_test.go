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
