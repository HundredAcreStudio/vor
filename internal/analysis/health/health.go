// Package health is vor's code-health engine. It runs deterministic
// biomarkers over parser output and emits per-finding + per-file metric
// records that mirror the health_findings and health_file_metrics tables.
//
// Mirrors the Python `core.analysis.health` package — see docs/PORTING_PLAN.md
// §5 for the full biomarker roster. Pass A lands the two biomarkers with
// the highest signal per LOC of code: cyclomatic complexity and long
// functions. Additional biomarkers (deep nesting, brain methods, primitive
// obsession, duplication, untested hotspots, hidden coupling, etc.) follow
// in subsequent passes.
package health

import (
	"fmt"
	"strings"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
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
	BiomarkerHighComplexity    = "high_complexity"
	BiomarkerLongFunction      = "long_function"
	BiomarkerDeepNesting       = "deep_nesting"
	BiomarkerGodClass          = "god_class"
	BiomarkerUntestedHotspot   = "untested_hotspot"
	BiomarkerBrainMethod       = "brain_method"
	BiomarkerHiddenCoupling    = "hidden_coupling"
	BiomarkerLongParameterList = "long_parameter_list"
	BiomarkerFeatureEnvy       = "feature_envy"
	BiomarkerShotgunSurgery    = "shotgun_surgery"
)

// AllBiomarkers lists every biomarker type, for UIs that let users select
// which checks to exclude. Kept here so it can't drift from the constants.
func AllBiomarkers() []string {
	return []string{
		BiomarkerDuplication,
		BiomarkerHighComplexity,
		BiomarkerLongFunction,
		BiomarkerDeepNesting,
		BiomarkerGodClass,
		BiomarkerUntestedHotspot,
		BiomarkerBrainMethod,
		BiomarkerHiddenCoupling,
		BiomarkerLongParameterList,
		BiomarkerFeatureEnvy,
		BiomarkerShotgunSurgery,
	}
}

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

	// UntestedCoveragePct: when imported coverage is available, a hotspot
	// with line coverage below this percentage is flagged untested.
	UntestedCoveragePct float64

	// LongParameterList / LongParameterListHigh: a function with ≥ this
	// many parameters is flagged medium / high. A primitive-obsession and
	// data-clump proxy.
	LongParameterList     int
	LongParameterListHigh int

	// FeatureEnvyCalls: a method making ≥ this many calls to a single
	// external receiver (more than to its own class) is flagged.
	FeatureEnvyCalls int

	// ShotgunSurgeryFiles: a file that co-changes with ≥ this many distinct
	// other files is flagged — a change here tends to ripple widely.
	ShotgunSurgeryFiles int
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
		UntestedCoveragePct:   50,
		LongParameterList:     5,
		LongParameterListHigh: 8,
		FeatureEnvyCalls:      4,
		ShotgunSurgeryFiles:   8,
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

	// Coverage maps a file path to its line-coverage percentage (0..100)
	// from an imported report. When present it makes untested_hotspot
	// authoritative: a hotspot is "untested" when its coverage is below
	// Thresholds.UntestedCoveragePct, regardless of paired-test-file
	// heuristics. Optional — nil falls back to the paired-test heuristic.
	Coverage map[string]float64

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

	// DuplicationMaxCluster suppresses duplicate clusters appearing at
	// more than this many locations (idiomatic/boilerplate blocks).
	// Zero -> default (8).
	DuplicationMaxCluster int

	// Exclude suppresses biomarker findings for files matching a glob or
	// path prefix, optionally limited to specific biomarkers. Sourced from
	// the config health_rules. Empty = nothing suppressed.
	Exclude []ExcludeRule
}

// Result is the bundle returned by Analyze.
type Result struct {
	Findings    []Finding
	FileMetrics []FileMetric
}

// Analyze runs all biomarkers over the parsed files and returns Findings +
// per-file aggregates.
func (a *Analyzer) Analyze(files []models.ParsedFile) Result {
	t := a.resolveThresholds()
	testPairs := buildTestPairs(files)

	findings := a.globalFindings(files, testPairs, t)
	for _, pf := range files {
		findings = append(findings, a.localFindings(pf, t)...)
	}
	findings = a.applyExcludes(findings)
	return Result{Findings: findings, FileMetrics: a.fileMetrics(files, testPairs, findings)}
}

// fileLocalBiomarkers are the biomarkers whose findings depend only on a single
// file's own parse (no git, no graph, no cross-file comparison). They can be
// recomputed per changed file independently. The rest — duplication,
// hidden_coupling, untested_hotspot, shotgun_surgery — couple files together or
// depend on commit-boundary git data, so they're recomputed globally.
var fileLocalBiomarkers = map[string]bool{
	BiomarkerHighComplexity:    true,
	BiomarkerLongFunction:      true,
	BiomarkerDeepNesting:       true,
	BiomarkerGodClass:          true,
	BiomarkerLongParameterList: true,
	BiomarkerFeatureEnvy:       true,
	BiomarkerBrainMethod:       true,
}

// IsFileLocalBiomarker reports whether a biomarker is recomputed per-file (vs
// globally) by AnalyzeIncremental.
func IsFileLocalBiomarker(biomarker string) bool { return fileLocalBiomarkers[biomarker] }

// AnalyzeIncremental produces the same Result as Analyze(files) but reuses the
// prior run's file-local findings for files NOT in `changed`, only re-running
// the per-file biomarkers for changed files. Global (cross-file / git-derived)
// findings are always recomputed since one file's change can shift them
// anywhere. Metrics are recomputed from the merged finding set.
//
// Invariant (covered by tests): when `changed` is exactly the set of files
// whose parse differs from the run that produced `prior`, the result equals
// Analyze(files). Exclude rules are re-applied to reused findings; a change to
// the exclude rules themselves should trigger a full Analyze.
func (a *Analyzer) AnalyzeIncremental(files []models.ParsedFile, changed map[string]bool, prior Result) Result {
	t := a.resolveThresholds()
	testPairs := buildTestPairs(files)

	findings := a.globalFindings(files, testPairs, t)

	priorLocal := map[string][]Finding{}
	for _, f := range prior.Findings {
		if fileLocalBiomarkers[f.BiomarkerType] {
			priorLocal[f.FilePath] = append(priorLocal[f.FilePath], f)
		}
	}
	for _, pf := range files {
		if path := pf.FileInfo.Path; changed[path] {
			findings = append(findings, a.localFindings(pf, t)...)
		} else {
			findings = append(findings, priorLocal[path]...)
		}
	}
	findings = a.applyExcludes(findings)
	return Result{Findings: findings, FileMetrics: a.fileMetrics(files, testPairs, findings)}
}

func (a *Analyzer) resolveThresholds() Thresholds {
	if a.Thresholds == (Thresholds{}) {
		return DefaultThresholds()
	}
	return a.Thresholds
}

// globalFindings computes the cross-file / git-derived biomarkers over the
// whole file set: hidden_coupling, duplication, and the per-file git findings
// (untested_hotspot, shotgun_surgery).
func (a *Analyzer) globalFindings(files []models.ParsedFile, testPairs map[string]bool, t Thresholds) []Finding {
	emittedPairs := map[string]bool{}
	out := a.computeHiddenCoupling(files, emittedPairs)
	out = append(out, a.computeDuplication(files)...)
	for _, pf := range files {
		out = append(out, a.fileFindings(pf, testPairs, t)...)
	}
	return out
}

// localFindings computes the file-local per-symbol biomarkers for one file.
func (a *Analyzer) localFindings(pf models.ParsedFile, t Thresholds) []Finding {
	methodsByParent := methodCounts(pf)
	callsByCaller := groupCallsByCaller(pf)
	var out []Finding
	for _, sym := range pf.Symbols {
		out = append(out, a.symbolFindings(pf, sym, methodsByParent, callsByCaller, t)...)
	}
	return out
}

// applyExcludes drops findings suppressed by the configured exclude rules
// (config health_rules), so they don't surface or drag a file's score.
func (a *Analyzer) applyExcludes(findings []Finding) []Finding {
	excludes := compileExcludes(a.Exclude)
	if len(excludes) == 0 {
		return findings
	}
	kept := findings[:0:0]
	for _, f := range findings {
		if !excluded(excludes, f.FilePath, f.BiomarkerType) {
			kept = append(kept, f)
		}
	}
	return kept
}

// fileMetrics computes the per-file aggregate metric for every file from the
// final (excluded-filtered) finding set.
func (a *Analyzer) fileMetrics(files []models.ParsedFile, testPairs map[string]bool, findings []Finding) []FileMetric {
	metrics := make([]FileMetric, 0, len(files))
	for _, pf := range files {
		metrics = append(metrics, fileMetric(pf, testPairs[pf.FileInfo.Path], findings))
	}
	return metrics
}

// fileFindings runs the per-file biomarkers (untested_hotspot, shotgun_surgery).
func (a *Analyzer) fileFindings(pf models.ParsedFile, testPairs map[string]bool, t Thresholds) []Finding {
	var out []Finding
	path := pf.FileInfo.Path

	// untested_hotspot: a known git hotspot that is under-tested. Imported
	// coverage is authoritative when present; else the paired-test heuristic.
	if _, isHot := a.HotspotPaths[path]; isHot && !pf.FileInfo.IsTest {
		if untested, reason, impact := a.untestedHotspot(path, testPairs, t); untested {
			out = append(out, Finding{
				FilePath: path, BiomarkerType: BiomarkerUntestedHotspot,
				Severity: SeverityHigh, HealthImpact: impact, Reason: reason,
			})
		}
	}

	// shotgun_surgery: edits to this file historically ripple to many others.
	if partners := distinctCount(a.CoChangePairs[path]); t.ShotgunSurgeryFiles > 0 && partners >= t.ShotgunSurgeryFiles {
		out = append(out, Finding{
			FilePath: path, BiomarkerType: BiomarkerShotgunSurgery, Severity: SeverityMedium,
			HealthImpact: clamp(1.0+float64(partners-t.ShotgunSurgeryFiles)*0.25, 1.0, 3.0),
			Reason:       fmt.Sprintf("co-changes with %d other files", partners),
			Details:      map[string]any{"coChangePartners": partners},
		})
	}
	return out
}

// methodCounts tallies methods per parent type for the god_class biomarker.
func methodCounts(pf models.ParsedFile) map[string]int {
	out := map[string]int{}
	for _, sym := range pf.Symbols {
		if sym.ParentName != nil && (sym.Kind == models.KindMethod || sym.Kind == models.KindFunction) {
			out[*sym.ParentName]++
		}
	}
	return out
}

// groupCallsByCaller indexes a file's call sites by enclosing symbol id.
func groupCallsByCaller(pf models.ParsedFile) map[string][]models.CallSite {
	out := map[string][]models.CallSite{}
	for _, c := range pf.Calls {
		if c.CallerSymbolID != nil {
			out[*c.CallerSymbolID] = append(out[*c.CallerSymbolID], c)
		}
	}
	return out
}

// symbolFindings runs the per-symbol biomarkers for one symbol.
func (a *Analyzer) symbolFindings(pf models.ParsedFile, sym models.Symbol, methodsByParent map[string]int, callsByCaller map[string][]models.CallSite, t Thresholds) []Finding {
	path := pf.FileInfo.Path
	lines := sym.EndLine - sym.StartLine + 1
	if lines < 0 {
		lines = 0
	}
	var out []Finding

	if hit, sev := complexityHit(sym.ComplexityEstimate, t); hit {
		out = append(out, Finding{FilePath: path, BiomarkerType: BiomarkerHighComplexity, Severity: sev,
			FunctionName: sym.Name, LineStart: sym.StartLine, LineEnd: sym.EndLine,
			HealthImpact: complexityImpact(sym.ComplexityEstimate, t),
			Reason:       fmt.Sprintf("cyclomatic complexity = %d", sym.ComplexityEstimate),
			Details:      map[string]any{"complexity": sym.ComplexityEstimate}})
	}
	if hit, sev := longFunctionHit(lines, t); hit {
		out = append(out, Finding{FilePath: path, BiomarkerType: BiomarkerLongFunction, Severity: sev,
			FunctionName: sym.Name, LineStart: sym.StartLine, LineEnd: sym.EndLine,
			HealthImpact: longFunctionImpact(lines, t),
			Reason:       fmt.Sprintf("function length = %d lines", lines),
			Details:      map[string]any{"lines": lines}})
	}
	if hit, sev := deepNestingHit(sym.NestingDepth, t); hit {
		out = append(out, Finding{FilePath: path, BiomarkerType: BiomarkerDeepNesting, Severity: sev,
			FunctionName: sym.Name, LineStart: sym.StartLine, LineEnd: sym.EndLine,
			HealthImpact: deepNestingImpact(sym.NestingDepth, t),
			Reason:       fmt.Sprintf("max nesting depth = %d", sym.NestingDepth),
			Details:      map[string]any{"nesting": sym.NestingDepth}})
	}
	// brain_method: bad on all three axes at once — the most refactor-worthy.
	if isBrainMethod(sym.ComplexityEstimate, sym.NestingDepth, lines, t) {
		out = append(out, Finding{FilePath: path, BiomarkerType: BiomarkerBrainMethod, Severity: SeverityHigh,
			FunctionName: sym.Name, LineStart: sym.StartLine, LineEnd: sym.EndLine, HealthImpact: 4.0,
			Reason: fmt.Sprintf("complexity=%d, nesting=%d, lines=%d (all above brain-method thresholds)",
				sym.ComplexityEstimate, sym.NestingDepth, lines),
			Details: map[string]any{"complexity": sym.ComplexityEstimate, "nesting": sym.NestingDepth, "lines": lines}})
	}
	if classKind(sym.Kind) {
		methodCount := methodsByParent[sym.Name]
		if hit, sev := godClassHit(methodCount, t); hit {
			out = append(out, Finding{FilePath: path, BiomarkerType: BiomarkerGodClass, Severity: sev,
				FunctionName: sym.Name, LineStart: sym.StartLine, LineEnd: sym.EndLine,
				HealthImpact: godClassImpact(methodCount, t),
				Reason:       fmt.Sprintf("%s has %d methods", sym.Name, methodCount),
				Details:      map[string]any{"methodCount": methodCount}})
		}
	}
	if sym.Kind != models.KindFunction && sym.Kind != models.KindMethod {
		return out
	}

	// long_parameter_list: many parameters hint at primitive obsession.
	if n := countSignatureParams(sym.Signature); t.LongParameterList > 0 && n >= t.LongParameterList {
		sev := SeverityMedium
		if n >= t.LongParameterListHigh {
			sev = SeverityHigh
		}
		out = append(out, Finding{FilePath: path, BiomarkerType: BiomarkerLongParameterList, Severity: sev,
			FunctionName: sym.Name, LineStart: sym.StartLine, LineEnd: sym.EndLine,
			HealthImpact: clamp(0.5+float64(n-t.LongParameterList)*0.4, 0.5, 3.0),
			Reason:       fmt.Sprintf("%d parameters", n),
			Details:      map[string]any{"parameters": n}})
	}

	// feature_envy: a method more interested in another class than its own.
	if sym.Kind == models.KindMethod && sym.ParentName != nil && !pf.FileInfo.IsTest {
		if envied, recv, n := featureEnvy(sym, callsByCaller[sym.ID], t); envied {
			out = append(out, Finding{FilePath: path, BiomarkerType: BiomarkerFeatureEnvy, Severity: SeverityMedium,
				FunctionName: sym.Name, LineStart: sym.StartLine, LineEnd: sym.EndLine,
				HealthImpact: clamp(1.0+float64(n-t.FeatureEnvyCalls)*0.3, 1.0, 3.0),
				Reason:       fmt.Sprintf("%d calls to %q (more than to its own type)", n, recv),
				Details:      map[string]any{"externalCalls": n, "receiver": recv}})
		}
	}
	return out
}

// fileMetric computes the per-file aggregate: max CCN/nesting, NLOC proxy,
// and the composite score (10 minus this file's summed finding impacts).
func fileMetric(pf models.ParsedFile, hasTest bool, findings []Finding) FileMetric {
	fm := FileMetric{FilePath: pf.FileInfo.Path, Score: 10.0, HasTestFile: hasTest}
	for _, sym := range pf.Symbols {
		if sym.ComplexityEstimate > fm.MaxCCN {
			fm.MaxCCN = sym.ComplexityEstimate
		}
		if sym.NestingDepth > fm.MaxNesting {
			fm.MaxNesting = sym.NestingDepth
		}
		if sym.EndLine > fm.NLOC {
			fm.NLOC = sym.EndLine
		}
	}
	var impact float64
	for _, f := range findings {
		if f.FilePath == pf.FileInfo.Path {
			impact += f.HealthImpact
		}
	}
	fm.Score = clamp(10.0-impact, 1.0, 10.0)
	return fm
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

// untestedHotspot decides whether a hotspot file counts as untested.
// Imported coverage wins when present for the file; otherwise the
// paired-test-file heuristic is used. Returns (untested, reason, impact).
func (a *Analyzer) untestedHotspot(path string, testPairs map[string]bool, t Thresholds) (bool, string, float64) {
	if a.Coverage != nil {
		if pct, ok := a.Coverage[path]; ok {
			if pct >= t.UntestedCoveragePct {
				return false, "", 0
			}
			// Impact grows as coverage drops: 2.0 at the threshold, up to
			// ~4.0 at zero coverage.
			impact := 2.0 + (t.UntestedCoveragePct-pct)/t.UntestedCoveragePct*2.0
			return true, fmt.Sprintf("high-churn file with %.0f%% line coverage (below %.0f%%)", pct, t.UntestedCoveragePct), clamp(impact, 2.0, 4.0)
		}
	}
	// No coverage data for this file: fall back to the heuristic.
	if testPairs[path] {
		return false, "", 0
	}
	return true, "high-churn file with no paired test file", 3.0
}

// distinctCount returns the number of unique entries in xs.
func distinctCount(xs []string) int {
	if len(xs) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		seen[x] = struct{}{}
	}
	return len(seen)
}

// countSignatureParams counts parameters in the first balanced parenthesis
// group of a signature line, commas counted at the group's top level only
// (so nested generics/tuples don't inflate the count). Returns 0 when there
// is no paren group or it is empty.
func countSignatureParams(sig string) int {
	start := strings.IndexByte(sig, '(')
	if start < 0 {
		return 0
	}
	depth := 0
	commas := 0
	hasContent := false
	for i := start; i < len(sig); i++ {
		switch sig[i] {
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			depth--
			if depth == 0 {
				if !hasContent {
					return 0
				}
				return commas + 1
			}
		case ',':
			if depth == 1 {
				commas++
			}
		default:
			if depth >= 1 && sig[i] != ' ' && sig[i] != '\t' {
				hasContent = true
			}
		}
	}
	if !hasContent {
		return 0
	}
	return commas + 1
}

// nonDomainReceivers are receiver names that represent standard-library
// packages, test harnesses, or generic builders — calling them many times
// is normal usage, not envy of another domain object. Matching is exact on
// the receiver token the parser captured.
var nonDomainReceivers = map[string]struct{}{
	// stdlib / common packages (receiver == package name)
	"fmt": {}, "strings": {}, "strconv": {}, "errors": {}, "bytes": {},
	"bufio": {}, "io": {}, "os": {}, "filepath": {}, "path": {}, "time": {},
	"sort": {}, "slices": {}, "maps": {}, "regexp": {}, "sync": {}, "math": {},
	"json": {}, "context": {}, "log": {}, "http": {}, "sql": {}, "rand": {},
	"utf8": {}, "unicode": {}, "reflect": {}, "exec": {}, "uuid": {},
	// test harnesses / assertion + mock libraries
	"t": {}, "b": {}, "require": {}, "assert": {}, "mock": {}, "expect": {},
}

// featureEnvy reports whether a method leans on a single external domain
// object more than on its own type. Returns (envied, receiverName, count).
//
// "External" excludes: self/this, the method's own receiver variable (Go
// idiom: the receiver var is the lowercased initial of the type, e.g. `s`
// for *Server), and non-domain receivers (stdlib packages, test harnesses,
// builders). The remaining dominant receiver, if it clears the threshold
// and accounts for the majority of qualifying calls, is the envied object.
func featureEnvy(sym models.Symbol, calls []models.CallSite, t Thresholds) (bool, string, int) {
	if t.FeatureEnvyCalls <= 0 || len(calls) == 0 {
		return false, "", 0
	}
	own := ""
	if sym.ParentName != nil {
		own = strings.ToLower(*sym.ParentName)
	}
	byReceiver := map[string]int{}
	total := 0
	for _, c := range calls {
		if c.ReceiverName == nil {
			continue
		}
		r := *c.ReceiverName
		if isSelfReceiver(r, own) {
			continue
		}
		if _, skip := nonDomainReceivers[r]; skip {
			continue
		}
		byReceiver[r]++
		total++
	}
	bestReceiver, bestN := "", 0
	for r, n := range byReceiver {
		if n > bestN {
			bestReceiver, bestN = r, n
		}
	}
	// Envy requires a dominant external domain receiver: above threshold
	// AND the majority of the method's qualifying calls go to it.
	if bestN >= t.FeatureEnvyCalls && bestN*2 >= total {
		return true, bestReceiver, bestN
	}
	return false, "", 0
}

// isSelfReceiver reports whether r refers to the method's own object: the
// literal self/this, or — by Go convention — a 1–2 char receiver variable
// that is the initial of the lowercased owning type name (s↔server, p↔provider).
func isSelfReceiver(r, ownLower string) bool {
	switch r {
	case "", "self", "this", "Self":
		return true
	}
	if ownLower != "" {
		if strings.EqualFold(r, ownLower) {
			return true
		}
		if len(r) <= 2 && strings.HasPrefix(ownLower, strings.ToLower(r)) {
			return true
		}
	}
	return false
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
