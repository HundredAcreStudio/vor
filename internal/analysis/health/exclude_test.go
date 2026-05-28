package health_test

import (
	"testing"

	"github.com/HundredAcreStudio/vor/internal/analysis/health"
	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

// complexSym builds a symbol that trips high_complexity + long_function.
func complexSym() models.Symbol {
	return models.Symbol{
		ID: "x::big", Name: "big", Kind: models.KindFunction,
		ComplexityEstimate: 25, StartLine: 1, EndLine: 90, Language: "go",
	}
}

func fileWith(path string, sym models.Symbol) models.ParsedFile {
	return models.ParsedFile{
		FileInfo: models.FileInfo{Path: path, Language: "go"},
		Symbols:  []models.Symbol{sym},
	}
}

func biomarkersFor(res health.Result, path string) map[string]bool {
	out := map[string]bool{}
	for _, f := range res.Findings {
		if f.FilePath == path {
			out[f.BiomarkerType] = true
		}
	}
	return out
}

func TestExclude_PerBiomarker(t *testing.T) {
	a := &health.Analyzer{
		Exclude: []health.ExcludeRule{
			{Pattern: "**/*_test.go", Biomarkers: []string{"high_complexity"}},
		},
	}
	res := a.Analyze([]models.ParsedFile{
		fileWith("pkg/svc_test.go", complexSym()),
		fileWith("pkg/svc.go", complexSym()),
	})

	test := biomarkersFor(res, "pkg/svc_test.go")
	if test["high_complexity"] {
		t.Error("high_complexity should be suppressed in *_test.go")
	}
	if !test["long_function"] {
		t.Error("long_function should still fire in *_test.go (not excluded)")
	}
	// Production file is untouched.
	prod := biomarkersFor(res, "pkg/svc.go")
	if !prod["high_complexity"] || !prod["long_function"] {
		t.Errorf("production file should keep all findings: %v", prod)
	}
}

func TestExclude_WholeFileWildcard(t *testing.T) {
	a := &health.Analyzer{
		Exclude: []health.ExcludeRule{
			{Pattern: "testdata/**", Biomarkers: []string{"*"}},
		},
	}
	res := a.Analyze([]models.ParsedFile{
		fileWith("testdata/fixture.go", complexSym()),
		fileWith("main.go", complexSym()),
	})
	if len(biomarkersFor(res, "testdata/fixture.go")) != 0 {
		t.Error("testdata file should have all biomarkers suppressed")
	}
	if len(biomarkersFor(res, "main.go")) == 0 {
		t.Error("main.go should still be analyzed")
	}
}

func TestExclude_PathPrefix(t *testing.T) {
	a := &health.Analyzer{
		Exclude: []health.ExcludeRule{{Path: "generated/"}}, // no biomarkers = all
	}
	res := a.Analyze([]models.ParsedFile{
		fileWith("generated/api.go", complexSym()),
		fileWith("hand/written.go", complexSym()),
	})
	if len(biomarkersFor(res, "generated/api.go")) != 0 {
		t.Error("generated/ prefix should suppress all biomarkers")
	}
	if len(biomarkersFor(res, "hand/written.go")) == 0 {
		t.Error("non-matching path should be analyzed")
	}
}

func TestExclude_RemovesScoreImpact(t *testing.T) {
	// An excluded file should score a perfect 10 — suppressed findings must
	// not drag the file metric.
	a := &health.Analyzer{
		Exclude: []health.ExcludeRule{{Pattern: "**/*_test.go"}},
	}
	res := a.Analyze([]models.ParsedFile{fileWith("z_test.go", complexSym())})
	for _, fm := range res.FileMetrics {
		if fm.FilePath == "z_test.go" && fm.Score != 10.0 {
			t.Errorf("excluded file score = %.1f, want 10.0 (no impact)", fm.Score)
		}
	}
}
