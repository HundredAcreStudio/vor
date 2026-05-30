package health_test

import (
	"fmt"
	"sort"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/analysis/health"
	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

// fn builds a function symbol with the given complexity/nesting/line span.
func fn(name string, ccn, nesting, start, end int) models.Symbol {
	return models.Symbol{
		ID: name, Name: name, Kind: models.KindFunction,
		ComplexityEstimate: ccn, NestingDepth: nesting,
		StartLine: start, EndLine: end,
		Visibility: models.VisibilityPublic, Language: "go",
	}
}

// fileWith builds a parsed file at path with the given symbols.
func pfile(path string, syms ...models.Symbol) models.ParsedFile {
	return models.ParsedFile{FileInfo: models.FileInfo{Path: path, Language: "go"}, Symbols: syms}
}

// findingKey canonicalizes a finding for multiset comparison.
func findingKey(f health.Finding) string {
	return fmt.Sprintf("%s|%s|%s|%d-%d|%.3f|%s",
		f.FilePath, f.BiomarkerType, f.FunctionName, f.LineStart, f.LineEnd, f.HealthImpact, f.Severity)
}

// assertEquivalent fails unless AnalyzeIncremental(files, changed, prior)
// yields the same findings (as a multiset) and per-file scores as a full
// Analyze(files) — the core incremental-correctness invariant.
func assertEquivalent(t *testing.T, a *health.Analyzer, files []models.ParsedFile, changed map[string]bool, prior health.Result) {
	t.Helper()
	inc := a.AnalyzeIncremental(files, changed, prior)
	full := a.Analyze(files)

	ik, fk := keys(inc.Findings), keys(full.Findings)
	if len(ik) != len(fk) {
		t.Fatalf("finding count: incremental=%d full=%d", len(ik), len(fk))
	}
	for i := range fk {
		if ik[i] != fk[i] {
			t.Errorf("finding mismatch at %d:\n incremental=%s\n full=%s", i, ik[i], fk[i])
		}
	}

	is, fs := scoreMap(inc.FileMetrics), scoreMap(full.FileMetrics)
	if len(is) != len(fs) {
		t.Fatalf("metric count: incremental=%d full=%d", len(is), len(fs))
	}
	for path, score := range fs {
		if is[path] != score {
			t.Errorf("score for %s: incremental=%.3f full=%.3f", path, is[path], score)
		}
	}
}

func keys(fs []health.Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = findingKey(f)
	}
	sort.Strings(out)
	return out
}

func scoreMap(ms []health.FileMetric) map[string]float64 {
	out := make(map[string]float64, len(ms))
	for _, m := range ms {
		out[m.FilePath] = m.Score
	}
	return out
}

func TestAnalyzeIncremental_Equivalence(t *testing.T) {
	a := &health.Analyzer{}

	// A two-file baseline: a.go clean-ish, b.go simple.
	oldFiles := []models.ParsedFile{
		pfile("a.go", fn("Alpha", 25, 2, 1, 80)), // high complexity
		pfile("b.go", fn("Beta", 3, 1, 1, 10)),   // clean
	}
	prior := a.Analyze(oldFiles)

	t.Run("changed file gains a biomarker", func(t *testing.T) {
		newFiles := []models.ParsedFile{
			pfile("a.go", fn("Alpha", 25, 2, 1, 80)),
			pfile("b.go", fn("Beta", 30, 5, 1, 200)), // now complex+long+nested
		}
		assertEquivalent(t, a, newFiles, map[string]bool{"b.go": true}, prior)
	})

	t.Run("changed file loses a biomarker", func(t *testing.T) {
		newFiles := []models.ParsedFile{
			pfile("a.go", fn("Alpha", 3, 1, 1, 12)), // now clean
			pfile("b.go", fn("Beta", 3, 1, 1, 10)),
		}
		assertEquivalent(t, a, newFiles, map[string]bool{"a.go": true}, prior)
	})

	t.Run("file added", func(t *testing.T) {
		newFiles := []models.ParsedFile{
			pfile("a.go", fn("Alpha", 25, 2, 1, 80)),
			pfile("b.go", fn("Beta", 3, 1, 1, 10)),
			pfile("c.go", fn("Gamma", 40, 6, 1, 300)),
		}
		assertEquivalent(t, a, newFiles, map[string]bool{"c.go": true}, prior)
	})

	t.Run("file removed", func(t *testing.T) {
		newFiles := []models.ParsedFile{
			pfile("b.go", fn("Beta", 3, 1, 1, 10)),
		}
		// No file in newFiles changed content; a.go is simply gone.
		assertEquivalent(t, a, newFiles, map[string]bool{}, prior)
	})

	t.Run("no-op (nothing changed) reuses everything", func(t *testing.T) {
		assertEquivalent(t, a, oldFiles, map[string]bool{}, prior)
	})

	t.Run("multiple files, only one changed", func(t *testing.T) {
		old3 := []models.ParsedFile{
			pfile("a.go", fn("A", 25, 2, 1, 80)),
			pfile("b.go", fn("B", 3, 1, 1, 10)),
			pfile("c.go", fn("C", 12, 1, 1, 30)),
		}
		p3 := a.Analyze(old3)
		new3 := []models.ParsedFile{
			pfile("a.go", fn("A", 25, 2, 1, 80)),     // unchanged → reused
			pfile("b.go", fn("B", 70, 8, 1, 400)),    // changed
			pfile("c.go", fn("C", 12, 1, 1, 30)),     // unchanged → reused
		}
		assertEquivalent(t, a, new3, map[string]bool{"b.go": true}, p3)
	})
}

func TestAnalyzeIncremental_RecomputesGlobalFindings(t *testing.T) {
	// hidden_coupling is a global (cross-file) biomarker: a.go co-changes with
	// b.go but there's no graph edge. It must be recomputed by the incremental
	// path even when only b.go changed — equivalence covers this.
	a := &health.Analyzer{
		CoChangePairs: map[string][]string{"a.go": {"b.go"}},
		// no GraphEdges → the pair has no explicit dependency → hidden_coupling
	}
	oldFiles := []models.ParsedFile{
		pfile("a.go", fn("A", 3, 1, 1, 10)),
		pfile("b.go", fn("B", 3, 1, 1, 10)),
	}
	prior := a.Analyze(oldFiles)
	// Sanity: the baseline actually produced a hidden_coupling finding.
	if !hasBiomarker(prior.Findings, health.BiomarkerHiddenCoupling) {
		t.Fatalf("expected a hidden_coupling finding in the baseline; got %v", prior.Findings)
	}

	newFiles := []models.ParsedFile{
		pfile("a.go", fn("A", 3, 1, 1, 10)),
		pfile("b.go", fn("B", 40, 5, 1, 200)), // changed (gains file-local findings)
	}
	assertEquivalent(t, a, newFiles, map[string]bool{"b.go": true}, prior)
}

func hasBiomarker(fs []health.Finding, bm string) bool {
	for _, f := range fs {
		if f.BiomarkerType == bm {
			return true
		}
	}
	return false
}

func TestIsFileLocalBiomarker(t *testing.T) {
	local := []string{
		health.BiomarkerHighComplexity, health.BiomarkerLongFunction, health.BiomarkerDeepNesting,
		health.BiomarkerGodClass, health.BiomarkerLongParameterList, health.BiomarkerFeatureEnvy,
		health.BiomarkerBrainMethod,
	}
	for _, b := range local {
		if !health.IsFileLocalBiomarker(b) {
			t.Errorf("%s should be file-local", b)
		}
	}
	global := []string{
		health.BiomarkerDuplication, health.BiomarkerHiddenCoupling,
		health.BiomarkerUntestedHotspot, health.BiomarkerShotgunSurgery,
	}
	for _, b := range global {
		if health.IsFileLocalBiomarker(b) {
			t.Errorf("%s should be global, not file-local", b)
		}
	}
}
