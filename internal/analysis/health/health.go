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
const (
	BiomarkerHighComplexity = "high_complexity"
	BiomarkerLongFunction   = "long_function"
	BiomarkerDeepNesting    = "deep_nesting"
	BiomarkerGodClass       = "god_class"
)

// Finding is one biomarker hit. Persisted into health_findings.
type Finding struct {
	FilePath     string
	BiomarkerType string
	Severity     Severity
	FunctionName string
	LineStart    int
	LineEnd      int
	HealthImpact float64 // 0–10, contribution to file score reduction
	Reason       string
	Details      map[string]any
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
	}
}

// Analyzer runs biomarkers over a set of parsed files.
type Analyzer struct {
	Thresholds Thresholds
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

	for _, pf := range files {
		fm := FileMetric{
			FilePath: pf.FileInfo.Path,
			Score:    10.0,
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
					Reason: fmt.Sprintf("cyclomatic complexity = %d", sym.ComplexityEstimate),
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
					Reason: fmt.Sprintf("function length = %d lines", lines),
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
					Reason: fmt.Sprintf("max nesting depth = %d", sym.NestingDepth),
					Details: map[string]any{
						"nesting": sym.NestingDepth,
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
						Reason: fmt.Sprintf("%s has %d methods", sym.Name, methodCount),
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
