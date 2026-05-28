// Package health is repowise's code-health engine. It runs deterministic
// biomarkers over parser output and emits per-finding + per-file metric
// records that mirror the health_findings and health_file_metrics tables.
//
// Mirrors the Python `core.analysis.health` package — see PORTING_PLAN.md
// §5 for the full biomarker roster. Pass A lands the two biomarkers with
// the highest signal per LOC of code: cyclomatic complexity and long
// functions. Additional biomarkers (deep nesting, brain methods, primitive
// obsession, duplication, untested hotspots, hidden coupling, etc.) follow
// in subsequent passes.
package health

import (
	"fmt"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// Severity classifies a finding's urgency. Mirrors the Python source.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// Biomarker names. Stored as biomarker_type on health_findings.
// BiomarkerDuplication is declared in duplication.go (lives next to
// its implementation).
const (
	BiomarkerHighComplexity  = "high_complexity"
	BiomarkerLongFunction    = "long_function"
	BiomarkerDeepNesting     = "deep_nesting"
	BiomarkerGodClass        = "god_class"
	BiomarkerUntestedHotspot = "untested_hotspot"
	BiomarkerBrainMethod     = "brain_method"
	BiomarkerHiddenCoupling  = "hidden_coupling"
)

// Finding is one biomarker hit. Persisted into health_findings.
type Finding struct {
	FilePath      string
	BiomarkerType string
	Severity      Severity
	FunctionName  string
	LineStart     int
	LineEnd       int
	HealthImpact  float64 // 0–10, contribution to file score reduction
	Reason        string
	Details       map[string]any
}

// FileMetric is one per-file aggregate row. Persisted into
// health_file_metrics.
type FileMetric struct {
	FilePath    string
	Score       float64 // 1–10, ten = perfect
	MaxCCN      int
	MaxNesting  int
	NLOC        int
	HasTestFile bool
	Module      string
}

// Thresholds are the cut-offs for each biomarker. Values default to the
// Python implementation's recommendations.
type Thresholds struct {
	// ComplexityWarning: CCN ≥ this is flagged medium.
	ComplexityWarning int
	// ComplexityHigh: CCN ≥ this is flagged high.
	ComplexityHigh int
	// ComplexityCritical: CCN ≥ this is flagged critical.
	ComplexityCritical int

	// LongFunctionLines: function lines ≥ this is flagged medium.
	LongFunctionLines int
	// VeryLongFunctionLines: function lines ≥ this is flagged high.
	VeryLongFunctionLines int

	// NestingWarning: nesting depth ≥ this is flagged medium.
	NestingWarning int
	// NestingHigh: nesting depth ≥ this is flagged high.
	NestingHigh int

	// GodClassMethods: classes with ≥ this many methods are flagged medium.
	GodClassMethods int
	// GodClassMethodsHigh: classes with ≥ this many methods are flagged high.
	GodClassMethodsHigh int

	// BrainMethodCCN / BrainMethodNesting / BrainMethodLines are the
	// thresholds a function must exceed simultaneously to be flagged as
	// a brain_method. The composite captures "doing too much" better than
	// any single dimension. Defaults pick mid-tier values from each axis.
	BrainMethodCCN     int
	BrainMethodNesting int
	BrainMethodLines   int
}

// DefaultThresholds returns the recommended values.
func DefaultThresholds() Thresholds {
	return Thresholds{
		ComplexityWarning:     10,
		ComplexityHigh:        20,
		ComplexityCritical:    50,
		LongFunctionLines:     60,
		VeryLongFunctionLines: 120,
		NestingWarning:        4,
		NestingHigh:           6,
		GodClassMethods:       15,
		GodClassMethodsHigh:   25,
		BrainMethodCCN:        10,
		BrainMethodNesting:    3,
		BrainMethodLines:      50,
	}
}

// Analyzer runs biomarkers over a set of parsed files.
type Analyzer struct {
	Thresholds Thresholds

	// HotspotPaths is the set of file paths flagged as git hotspots,
	// used by the untested_hotspot biomarker. Pass the output of git
	// intelligence here. Optional — when empty, the biomarker won't
	// fire.
	HotspotPaths map[string]struct{}

	// CoChangePairs maps a file path to the paths it has historically
	// co-changed with. Caller is expected to pre-filter by minimum
	// co-change count. Used by hidden_coupling.
	CoChangePairs map[string][]string

	// GraphEdges is the set of (source, target) file pairs that have
	// a graph edge (any direction, any type). Used by hidden_coupling
	// to detect co-changing files that DON'T already have an explicit
	// dependency.
	GraphEdges map[string]map[string]bool

	// SourceLoader returns the raw bytes of a file by repo-relative
	// path. Required for the duplication biomarker; when nil, that
	// biomarker is skipped silently (other biomarkers don't need
	// source bytes). The pipeline wires this to os.ReadFile under
	// the repo root.
	SourceLoader SourceLoader

	// DuplicationWindow overrides the default 6-line window used by
	// the duplication biomarker. Zero -> default. Values <3 are
	// clamped up to 3 — below that the signal/noise ratio is hostile.
	DuplicationWindow int
}

// Result is the bundle returned by Analyze.
type Result struct {
	Findings    []Finding
	FileMetrics []FileMetric
}

// Analyze runs all biomarkers over the parsed files and returns Findings +
// per-file aggregates.
func (a *Analyzer) Analyze(files []models.ParsedFile) Result {
	thresholds := a.Thresholds
	if thresholds == (Thresholds{}) {
		thresholds = DefaultThresholds()
	}

	var findings []Finding
	metrics := make([]FileMetric, 0, len(files))

	// Pre-compute "files that have a paired test file" so untested_hotspot
	// can skip well-covered hotspots.
	testPairs := buildTestPairs(files)

	// hidden_coupling: one finding per unique unordered (a, b) pair. Track
	// emitted pairs so we don't double-flag from both sides.
	emittedPairs := map[string]bool{}
	hiddenCouplingFindings := a.computeHiddenCoupling(files, emittedPairs)
	findings = append(findings, hiddenCouplingFindings...)

	// duplication: cross-file Rabin-Karp on normalized line windows.
	// Skipped silently when SourceLoader is nil.
	findings = append(findings, a.computeDuplication(files)...)

	for _, pf := range files {
		fm := FileMetric{
			FilePath:    pf.FileInfo.Path,
			Score:       10.0,
			HasTestFile: testPairs[pf.FileInfo.Path],
		}

		// untested_hotspot: file is a known git hotspot AND has no paired
		// test file. One finding per file, not per symbol.
		if _, isHot := a.HotspotPaths[pf.FileInfo.Path]; isHot && !pf.FileInfo.IsTest && !testPairs[pf.FileInfo.Path] {
			findings = append(findings, Finding{
				FilePath:      pf.FileInfo.Path,
				BiomarkerType: BiomarkerUntestedHotspot,
				Severity:      SeverityHigh,
				HealthImpact:  3.0,
				Reason:        "high-churn file with no paired test file",
			})
		}

		// Count methods per parent class for the god_class biomarker.
		// One pre-pass keeps the per-symbol loop O(n).
		methodsByParent := map[string]int{}
		for _, sym := range pf.Symbols {
			if sym.ParentName != nil && (sym.Kind == models.KindMethod || sym.Kind == models.KindFunction) {
				methodsByParent[*sym.ParentName]++
			}
		}

		// Per-symbol biomarkers + file metric accumulation.
		for _, sym := range pf.Symbols {
			lines := sym.EndLine - sym.StartLine + 1
			if lines < 0 {
				lines = 0
			}
			if sym.ComplexityEstimate > fm.MaxCCN {
				fm.MaxCCN = sym.ComplexityEstimate
			}
			if sym.NestingDepth > fm.MaxNesting {
				fm.MaxNesting = sym.NestingDepth
			}

			if hit, sev := complexityHit(sym.ComplexityEstimate, thresholds); hit {
				findings = append(findings, Finding{
					FilePath:      pf.FileInfo.Path,
					BiomarkerType: BiomarkerHighComplexity,
					Severity:      sev,
					FunctionName:  sym.Name,
					LineStart:     sym.StartLine,
					LineEnd:       sym.EndLine,
					HealthImpact:  complexityImpact(sym.ComplexityEstimate, thresholds),
					Reason:        fmt.Sprintf("cyclomatic complexity = %d", sym.ComplexityEstimate),
					Details: map[string]any{
						"complexity": sym.ComplexityEstimate,
					},
				})
			}

			if hit, sev := longFunctionHit(lines, thresholds); hit {
				findings = append(findings, Finding{
					FilePath:      pf.FileInfo.Path,
					BiomarkerType: BiomarkerLongFunction,
					Severity:      sev,
					FunctionName:  sym.Name,
					LineStart:     sym.StartLine,
					LineEnd:       sym.EndLine,
					HealthImpact:  longFunctionImpact(lines, thresholds),
					Reason:        fmt.Sprintf("function length = %d lines", lines),
					Details: map[string]any{
						"lines": lines,
					},
				})
			}

			if hit, sev := deepNestingHit(sym.NestingDepth, thresholds); hit {
				findings = append(findings, Finding{
					FilePath:      pf.FileInfo.Path,
					BiomarkerType: BiomarkerDeepNesting,
					Severity:      sev,
					FunctionName:  sym.Name,
					LineStart:     sym.StartLine,
					LineEnd:       sym.EndLine,
					HealthImpact:  deepNestingImpact(sym.NestingDepth, thresholds),
					Reason:        fmt.Sprintf("max nesting depth = %d", sym.NestingDepth),
					Details: map[string]any{
						"nesting": sym.NestingDepth,
					},
				})
			}

			// brain_method: bad on all three axes simultaneously (CCN +
			// nesting + length). High severity always — these are the
			// most refactor-worthy functions in any codebase.
			if isBrainMethod(sym.ComplexityEstimate, sym.NestingDepth, lines, thresholds) {
				findings = append(findings, Finding{
					FilePath:      pf.FileInfo.Path,
					BiomarkerType: BiomarkerBrainMethod,
					Severity:      SeverityHigh,
					FunctionName:  sym.Name,
					LineStart:     sym.StartLine,
					LineEnd:       sym.EndLine,
					HealthImpact:  4.0,
					Reason: fmt.Sprintf("complexity=%d, nesting=%d, lines=%d (all above brain-method thresholds)",
						sym.ComplexityEstimate, sym.NestingDepth, lines),
					Details: map[string]any{
						"complexity": sym.ComplexityEstimate,
						"nesting":    sym.NestingDepth,
						"lines":      lines,
					},
				})
			}

			// god_class fires once per class-like symbol whose method count
			// exceeds the threshold.
			if classKind(sym.Kind) {
				methodCount := methodsByParent[sym.Name]
				if hit, sev := godClassHit(methodCount, thresholds); hit {
					findings = append(findings, Finding{
						FilePath:      pf.FileInfo.Path,
						BiomarkerType: BiomarkerGodClass,
						Severity:      sev,
						FunctionName:  sym.Name,
						LineStart:     sym.StartLine,
						LineEnd:       sym.EndLine,
						HealthImpact:  godClassImpact(methodCount, thresholds),
						Reason:        fmt.Sprintf("%s has %d methods", sym.Name, methodCount),
						Details: map[string]any{
							"methodCount": methodCount,
						},
					})
				}
			}
		}

		// File NLOC: use the max EndLine across symbols as a coarse proxy.
		// A better estimate would count non-empty non-comment lines from
		// source; phase-out the proxy when the parser surfaces NLOC.
		for _, sym := range pf.Symbols {
			if sym.EndLine > fm.NLOC {
				fm.NLOC = sym.EndLine
			}
		}

		// Compute composite file score: subtract each finding's
		// HealthImpact, capped at zero.
		var impact float64
		for _, f := range findings {
			if f.FilePath != pf.FileInfo.Path {
				continue
			}
			impact += f.HealthImpact
		}
		fm.Score = clamp(10.0-impact, 1.0, 10.0)

		metrics = append(metrics, fm)
	}

	return Result{Findings: findings, FileMetrics: metrics}
}

// computeHiddenCoupling returns one Finding per unique (a, b) pair where:
//   - a and b are both files in the analyzed set
//   - a's CoChangePairs include b (caller pre-filters by min count)
//   - no graph edge between them in either direction
//
// The unordered pair (a,b) is emitted as a single finding tagged to file
// a (the lex-smaller of the two). emittedPairs tracks de-dup state so
// repeated calls (which currently can't happen) stay clean.
func (a *Analyzer) computeHiddenCoupling(files []models.ParsedFile, emittedPairs map[string]bool) []Finding {
	if len(a.CoChangePairs) == 0 {
		return nil
	}
	// Index file paths in the analyzed set so we only flag pairs we
	// actually parsed (saves noise from co-change with vendored files).
	inSet := map[string]bool{}
	for _, f := range files {
		inSet[f.FileInfo.Path] = true
	}

	var out []Finding
	for src, partners := range a.CoChangePairs {
		if !inSet[src] {
			continue
		}
		for _, partner := range partners {
			if !inSet[partner] {
				continue
			}
			lo, hi := src, partner
			if hi < lo {
				lo, hi = hi, lo
			}
			pairKey := lo + "\x00" + hi
			if emittedPairs[pairKey] {
				continue
			}
			if hasGraphEdge(a.GraphEdges, lo, hi) {
				continue
			}
			emittedPairs[pairKey] = true
			out = append(out, Finding{
				FilePath:      lo,
				BiomarkerType: BiomarkerHiddenCoupling,
				Severity:      SeverityMedium,
				HealthImpact:  1.5,
				Reason:        fmt.Sprintf("co-changes with %s but no graph edge between them", hi),
				Details: map[string]any{
					"partner": hi,
				},
			})
		}
	}
	return out
}

func hasGraphEdge(edges map[string]map[string]bool, a, b string) bool {
	if edges == nil {
		return false
	}
	if dst, ok := edges[a]; ok && dst[b] {
		return true
	}
	if dst, ok := edges[b]; ok && dst[a] {
		return true
	}
	return false
}

// isBrainMethod returns true when ALL three axes — complexity, nesting,
// and length — are above their brain-method thresholds simultaneously.
// Skips when any threshold is zero (the caller hasn't configured it).
func isBrainMethod(ccn, nesting, lines int, t Thresholds) bool {
	if t.BrainMethodCCN <= 0 || t.BrainMethodNesting <= 0 || t.BrainMethodLines <= 0 {
		return false
	}
	return ccn >= t.BrainMethodCCN &&
		nesting >= t.BrainMethodNesting &&
		lines >= t.BrainMethodLines
}

// buildTestPairs walks files and returns a map of production paths whose
// paired test file exists in the same set. Recognised pairings:
//
//	Go:     foo.go      ↔  foo_test.go
//	Python: foo.py      ↔  test_foo.py   (or foo_test.py)
//	JS/TS:  foo.ts      ↔  foo.test.ts   (or foo.spec.ts)
//	Java:   Foo.java    ↔  FooTest.java
func buildTestPairs(files []models.ParsedFile) map[string]bool {
	testsByProdPath := map[string]struct{}{}
	for _, f := range files {
		if !f.FileInfo.IsTest {
			continue
		}
		for _, prod := range productionSiblings(f.FileInfo.Path) {
			testsByProdPath[prod] = struct{}{}
		}
	}
	out := map[string]bool{}
	for _, f := range files {
		if f.FileInfo.IsTest {
			continue
		}
		if _, ok := testsByProdPath[f.FileInfo.Path]; ok {
			out[f.FileInfo.Path] = true
		}
	}
	return out
}

// productionSiblings returns possible production file paths a test path
// could be testing. Multiple candidates because conventions overlap.
func productionSiblings(testPath string) []string {
	dir, base := splitDirBase(testPath)
	ext := fileExtLocal(base)
	stem := base[:len(base)-len(ext)]
	cands := []string{}

	if ext == ".go" && hasSuffixLocal(stem, "_test") {
		cands = append(cands, joinDir(dir, stem[:len(stem)-5]+ext))
	}
	if ext == ".py" && hasPrefixLocal(stem, "test_") {
		cands = append(cands, joinDir(dir, stem[5:]+ext))
	}
	if ext == ".py" && hasSuffixLocal(stem, "_test") {
		cands = append(cands, joinDir(dir, stem[:len(stem)-5]+ext))
	}
	for _, suf := range []string{".test", ".spec"} {
		if hasSuffixLocal(stem, suf) {
			cands = append(cands, joinDir(dir, stem[:len(stem)-len(suf)]+ext))
		}
	}
	if ext == ".java" && hasSuffixLocal(stem, "Test") {
		cands = append(cands, joinDir(dir, stem[:len(stem)-4]+ext))
	}
	return cands
}

func splitDirBase(p string) (string, string) {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i+1], p[i+1:]
		}
	}
	return "", p
}
func fileExtLocal(base string) string {
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '.' {
			return base[i:]
		}
		if base[i] == '/' {
			return ""
		}
	}
	return ""
}
func hasPrefixLocal(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
func hasSuffixLocal(s, p string) bool { return len(s) >= len(p) && s[len(s)-len(p):] == p }
func joinDir(dir, base string) string {
	if dir == "" {
		return base
	}
	return dir + base
}

func complexityHit(ccn int, t Thresholds) (bool, Severity) {
	switch {
	case ccn >= t.ComplexityCritical:
		return true, SeverityCritical
	case ccn >= t.ComplexityHigh:
		return true, SeverityHigh
	case ccn >= t.ComplexityWarning:
		return true, SeverityMedium
	default:
		return false, ""
	}
}

func longFunctionHit(lines int, t Thresholds) (bool, Severity) {
	switch {
	case lines >= t.VeryLongFunctionLines:
		return true, SeverityHigh
	case lines >= t.LongFunctionLines:
		return true, SeverityMedium
	default:
		return false, ""
	}
}

func deepNestingHit(depth int, t Thresholds) (bool, Severity) {
	// A zero NestingWarning means the caller hasn't configured nesting and
	// doesn't want this biomarker — skip rather than flag every symbol.
	if t.NestingWarning <= 0 {
		return false, ""
	}
	switch {
	case t.NestingHigh > 0 && depth >= t.NestingHigh:
		return true, SeverityHigh
	case depth >= t.NestingWarning:
		return true, SeverityMedium
	default:
		return false, ""
	}
}

func deepNestingImpact(depth int, t Thresholds) float64 {
	if depth < t.NestingWarning {
		return 0
	}
	if depth >= t.NestingHigh {
		return 2
	}
	span := float64(t.NestingHigh - t.NestingWarning)
	if span <= 0 {
		return 1
	}
	progress := float64(depth-t.NestingWarning) / span
	return 1 + progress
}

// classKind returns true for symbol kinds that can host methods. Used by
// the god_class biomarker to decide where to look.
func classKind(k models.SymbolKind) bool {
	switch k {
	case models.KindClass, models.KindStruct, models.KindInterface, models.KindTrait, models.KindImpl:
		return true
	}
	return false
}

func godClassHit(methodCount int, t Thresholds) (bool, Severity) {
	if t.GodClassMethods <= 0 {
		return false, ""
	}
	switch {
	case t.GodClassMethodsHigh > 0 && methodCount >= t.GodClassMethodsHigh:
		return true, SeverityHigh
	case methodCount >= t.GodClassMethods:
		return true, SeverityMedium
	default:
		return false, ""
	}
}

func godClassImpact(methodCount int, t Thresholds) float64 {
	if methodCount < t.GodClassMethods {
		return 0
	}
	if methodCount >= t.GodClassMethodsHigh {
		return 2
	}
	span := float64(t.GodClassMethodsHigh - t.GodClassMethods)
	if span <= 0 {
		return 1
	}
	progress := float64(methodCount-t.GodClassMethods) / span
	return 1 + progress
}

// complexityImpact maps a CCN value to a 0-10 score reduction. Linear
// between Warning (impact 1) and Critical (impact 5). Above critical, the
// impact caps at 5 — beyond a point the function needs a refactor, not
// more shame piled on.
func complexityImpact(ccn int, t Thresholds) float64 {
	if ccn < t.ComplexityWarning {
		return 0
	}
	if ccn >= t.ComplexityCritical {
		return 5
	}
	span := float64(t.ComplexityCritical - t.ComplexityWarning)
	progress := float64(ccn-t.ComplexityWarning) / span
	return 1 + progress*4
}

func longFunctionImpact(lines int, t Thresholds) float64 {
	if lines < t.LongFunctionLines {
		return 0
	}
	if lines >= t.VeryLongFunctionLines {
		return 3
	}
	span := float64(t.VeryLongFunctionLines - t.LongFunctionLines)
	progress := float64(lines-t.LongFunctionLines) / span
	return 1 + progress*2
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
