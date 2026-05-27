package health_test

import (
	"strings"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/health"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// makeFile constructs a minimal ParsedFile referencing the given path
// + language; the actual source body is supplied via the loader the
// test passes to Analyzer.
func makeFile(path, lang string) models.ParsedFile {
	return models.ParsedFile{
		FileInfo: models.FileInfo{
			Path:     path,
			Language: models.LanguageTag(lang),
		},
	}
}

// loaderFor returns a SourceLoader serving the given path→source map.
func loaderFor(sources map[string]string) health.SourceLoader {
	return func(rel string) ([]byte, error) {
		body, ok := sources[rel]
		if !ok {
			return nil, nil // empty body — analyzer skips
		}
		return []byte(body), nil
	}
}

// findDuplications filters Findings to just the duplication entries.
func findDuplications(rs []health.Finding) []health.Finding {
	out := []health.Finding{}
	for _, f := range rs {
		if f.BiomarkerType == health.BiomarkerDuplication {
			out = append(out, f)
		}
	}
	return out
}

func TestDuplication_DetectsObviousCopyPaste(t *testing.T) {
	block := `func Calculate(x, y int) int {
	result := x + y
	if result > 100 {
		result = 100
	}
	return result
}`
	srcA := block + "\n\nvar _ = 1\n"
	srcB := "// unrelated header\n" + block + "\nvar _ = 2\n"

	res := (&health.Analyzer{
		SourceLoader: loaderFor(map[string]string{
			"a.go": srcA,
			"b.go": srcB,
		}),
	}).Analyze([]models.ParsedFile{
		makeFile("a.go", "go"),
		makeFile("b.go", "go"),
	})

	dups := findDuplications(res.Findings)
	if len(dups) == 0 {
		t.Fatalf("expected duplication findings, got none")
	}
	// Each duplicate cluster should produce one Finding per site.
	// With 2 sites we expect at least 2 findings.
	if len(dups) < 2 {
		t.Errorf("expected ≥2 findings (one per site), got %d", len(dups))
	}
	// Both files should appear among the FilePath fields.
	paths := map[string]bool{}
	for _, d := range dups {
		paths[d.FilePath] = true
	}
	if !paths["a.go"] || !paths["b.go"] {
		t.Errorf("expected both files in findings, got %v", paths)
	}
}

func TestDuplication_IgnoresWhitespaceAndComments(t *testing.T) {
	block := `result := compute(x, y)
processed := result * 2
final := wrap(processed)
report(final)
log("done", final)
return final`

	// Variant: heavier indent, trailing comments, extra blank lines.
	variant := "  result := compute(x, y)  // step 1\n" +
		"  processed := result * 2 // double\n" +
		"  final := wrap(processed)\n" +
		"  report(final)   \n" +
		"  log(\"done\", final)\n" +
		"  return final"

	res := (&health.Analyzer{
		SourceLoader: loaderFor(map[string]string{
			"a.go": "package x\n" + block + "\n",
			"b.go": "package y\n" + variant + "\n",
		}),
	}).Analyze([]models.ParsedFile{
		makeFile("a.go", "go"),
		makeFile("b.go", "go"),
	})

	dups := findDuplications(res.Findings)
	if len(dups) == 0 {
		t.Errorf("normalisation should have matched indent+comment variant: %d dups", len(dups))
	}
}

func TestDuplication_SkippedWithoutLoader(t *testing.T) {
	res := (&health.Analyzer{}).Analyze([]models.ParsedFile{
		makeFile("a.go", "go"), makeFile("b.go", "go"),
	})
	if len(findDuplications(res.Findings)) != 0 {
		t.Error("duplication biomarker should be inert when SourceLoader nil")
	}
}

func TestDuplication_NoMatchOnDistinctFiles(t *testing.T) {
	res := (&health.Analyzer{
		SourceLoader: loaderFor(map[string]string{
			"a.go": "func A() { return 1 }\nfunc B() { return 2 }\n",
			"b.go": "type T struct{}\nfunc (t T) Q() error { return nil }\n",
		}),
	}).Analyze([]models.ParsedFile{
		makeFile("a.go", "go"), makeFile("b.go", "go"),
	})
	if dups := findDuplications(res.Findings); len(dups) != 0 {
		t.Errorf("unrelated files should not produce duplication findings: %d", len(dups))
	}
}

func TestDuplication_DetectsIntraFileDup(t *testing.T) {
	// Same block appearing twice in one file should still be flagged.
	block := `result := compute(x, y)
processed := result * 2
final := wrap(processed)
report(final)
log("done", final)
return final`
	src := "func A() {\n" + block + "\n}\nfunc B() {\n" + block + "\n}\n"

	res := (&health.Analyzer{
		SourceLoader: loaderFor(map[string]string{"a.go": src}),
	}).Analyze([]models.ParsedFile{makeFile("a.go", "go")})

	dups := findDuplications(res.Findings)
	if len(dups) < 2 {
		t.Errorf("expected ≥2 findings for intra-file dup, got %d", len(dups))
	}
	// All entries must point at "a.go".
	for _, d := range dups {
		if d.FilePath != "a.go" {
			t.Errorf("unexpected FilePath = %q", d.FilePath)
		}
	}
}

func TestDuplication_LowSignalLinesDropped(t *testing.T) {
	// A block of mostly-blank lines + braces should NOT register, even
	// if it appears identically across files.
	src := "}\n\n}\n}\n\n}\n}\n"
	res := (&health.Analyzer{
		SourceLoader: loaderFor(map[string]string{
			"a.go": src + "func x() { return }\n",
			"b.go": src + "func y() { return }\n",
		}),
	}).Analyze([]models.ParsedFile{
		makeFile("a.go", "go"), makeFile("b.go", "go"),
	})
	dups := findDuplications(res.Findings)
	if len(dups) != 0 {
		t.Errorf("low-signal lines should not produce dups, got %d findings", len(dups))
	}
}

func TestDuplication_HonoursWindowOverride(t *testing.T) {
	// Two lines repeated identically; should not fire with default
	// 6-line window, but should with window=2.
	block := "x := 1\ny := 2\n"
	src := "func a() {\n" + block + "}\nfunc b() {\n" + block + "}\n"

	withDefault := (&health.Analyzer{
		SourceLoader: loaderFor(map[string]string{"f.go": src}),
	}).Analyze([]models.ParsedFile{makeFile("f.go", "go")})
	if dups := findDuplications(withDefault.Findings); len(dups) != 0 {
		t.Errorf("2-line block should not fire at default window: %d", len(dups))
	}

	withSmall := (&health.Analyzer{
		SourceLoader:      loaderFor(map[string]string{"f.go": src}),
		DuplicationWindow: 2,
	}).Analyze([]models.ParsedFile{makeFile("f.go", "go")})
	if dups := findDuplications(withSmall.Findings); len(dups) == 0 {
		t.Errorf("2-line block should fire with window=2")
	}
}

func TestDuplication_SeverityScalesWithClusterSize(t *testing.T) {
	block := `result := compute(x, y)
processed := result * 2
final := wrap(processed)
report(final)
log("done", final)
return final`
	sources := map[string]string{}
	files := []models.ParsedFile{}
	// 6 files, all sharing the same block — cluster size 6, severity high.
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		sources[name+".go"] = "package x\n" + block + "\n"
		files = append(files, makeFile(name+".go", "go"))
	}
	res := (&health.Analyzer{SourceLoader: loaderFor(sources)}).Analyze(files)
	dups := findDuplications(res.Findings)
	if len(dups) == 0 {
		t.Fatal("expected dups")
	}
	if dups[0].Severity != health.SeverityHigh {
		t.Errorf("expected SeverityHigh for cluster_size=6, got %s", dups[0].Severity)
	}
}

func TestDuplication_DetailsCarryDuplicateSites(t *testing.T) {
	block := `result := compute(x, y)
processed := result * 2
final := wrap(processed)
report(final)
log("done", final)
return final`
	res := (&health.Analyzer{
		SourceLoader: loaderFor(map[string]string{
			"a.go": "package a\n" + block + "\n",
			"b.go": "package b\n" + block + "\n",
		}),
	}).Analyze([]models.ParsedFile{
		makeFile("a.go", "go"), makeFile("b.go", "go"),
	})
	dups := findDuplications(res.Findings)
	if len(dups) == 0 {
		t.Fatal("expected dups")
	}
	first := dups[0]
	sites, ok := first.Details["duplicate_sites"].([]map[string]any)
	if !ok {
		t.Fatalf("Details[duplicate_sites] type = %T, want []map[string]any", first.Details["duplicate_sites"])
	}
	if len(sites) == 0 {
		t.Error("duplicate_sites should list the other locations")
	}
	if first.Details["window_lines"] != 6 {
		t.Errorf("window_lines = %v", first.Details["window_lines"])
	}
	if _, ok := first.Details["fingerprint_hex"].(string); !ok {
		t.Errorf("fingerprint_hex missing or wrong type: %v", first.Details["fingerprint_hex"])
	}
}

func TestDuplication_SkipsOversizedFiles(t *testing.T) {
	// A file >1MB should be ignored — generated content shouldn't blow
	// up the analyzer or produce useless dup findings.
	huge := strings.Repeat("var _ = 1\n", 200_000) // ~2 MB
	res := (&health.Analyzer{
		SourceLoader: loaderFor(map[string]string{"a.go": huge, "b.go": huge}),
	}).Analyze([]models.ParsedFile{
		makeFile("a.go", "go"), makeFile("b.go", "go"),
	})
	if dups := findDuplications(res.Findings); len(dups) != 0 {
		t.Errorf("oversized files should be skipped, got %d findings", len(dups))
	}
}
