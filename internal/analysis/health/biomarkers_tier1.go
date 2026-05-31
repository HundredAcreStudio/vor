package health

import (
	"fmt"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

// This file implements the Tier-1 expansion biomarkers (see
// docs/biomarker-expansion-plan.md §2): smells detectable from signals already
// present on models.ParsedFile, with no new Analyzer wiring. god_file is
// file-local; deep_inheritance is a global pass over the heritage relations of
// every parsed file.
//
// undocumented_api was part of the original Tier-1 set but was dropped: it
// depends on per-symbol docstrings the parsers don't extract, and "undocumented
// exported symbol" is a surface-quality lint better served by external tools
// (golangci-lint, ESLint) ingested via SARIF and fused — not reimplemented here.

// godFileFinding flags module bloat: a file with too many top-level symbols.
// Only symbols with no parent (free functions and type declarations, not their
// methods or fields) are counted, so a file built around a single large class
// is left to god_class — the two never double-count. Catches the bloat unit in
// procedural languages (Go, C, non-OO Python) where god_class never fires.
func godFileFinding(pf models.ParsedFile, t Thresholds) (Finding, bool) {
	if t.GodFileSymbols <= 0 {
		return Finding{}, false
	}
	topLevel := 0
	maxEnd := 0
	for _, sym := range pf.Symbols {
		if sym.EndLine > maxEnd {
			maxEnd = sym.EndLine
		}
		if sym.ParentName == nil && topLevelDeclKind(sym.Kind) {
			topLevel++
		}
	}
	if topLevel < t.GodFileSymbols {
		return Finding{}, false
	}
	sev := SeverityMedium
	if t.GodFileSymbolsHigh > 0 && topLevel >= t.GodFileSymbolsHigh {
		sev = SeverityHigh
	}
	return Finding{
		FilePath:      pf.FileInfo.Path,
		BiomarkerType: BiomarkerGodFile,
		Severity:      sev,
		HealthImpact:  godFileImpact(topLevel, t),
		Reason:        fmt.Sprintf("file declares %d top-level symbols", topLevel),
		Details: map[string]any{
			"topLevelSymbols": topLevel,
			"nloc":            maxEnd,
			"sizeBytes":       pf.FileInfo.SizeBytes,
		},
	}, true
}

// topLevelDeclKind reports whether a symbol kind counts toward god_file's
// top-level tally. Bare constants and variables are excluded: a file that is
// mostly a long enum or config block isn't "doing too much" in the way a file
// with dozens of functions or types is.
func topLevelDeclKind(k models.SymbolKind) bool {
	switch k {
	case models.KindConstant, models.KindVariable:
		return false
	}
	return true
}

func godFileImpact(topLevel int, t Thresholds) float64 {
	if topLevel < t.GodFileSymbols {
		return 0
	}
	if t.GodFileSymbolsHigh <= t.GodFileSymbols || topLevel >= t.GodFileSymbolsHigh {
		return 2.0
	}
	span := float64(t.GodFileSymbolsHigh - t.GodFileSymbols)
	progress := float64(topLevel-t.GodFileSymbols) / span
	return 1.0 + progress
}

// computeDeepInheritance is the global deep_inheritance pass. It builds the
// child→parents heritage forest from every file's Heritage relations, then
// flags each defined type whose depth of inheritance tree (longest ancestor
// chain) reaches the configured threshold. Reads only data already on the
// parsed files, so it needs no new Analyzer field — but it is global because a
// chain can span files.
func computeDeepInheritance(files []models.ParsedFile, t Thresholds) []Finding {
	if t.DeepInheritanceDIT <= 0 {
		return nil
	}

	// parents: child type name → its immediate supertypes (any heritage kind).
	parents := map[string][]string{}
	for _, pf := range files {
		for _, h := range pf.Heritage {
			parents[h.ChildName] = append(parents[h.ChildName], h.ParentName)
		}
	}
	if len(parents) == 0 {
		return nil
	}

	// loc: type name → where it is defined, so findings point at the subclass.
	// First definition wins on name collisions (best-effort; heritage is by name).
	type typeLoc struct {
		path               string
		startLine, endLine int
	}
	loc := map[string]typeLoc{}
	for _, pf := range files {
		for _, sym := range pf.Symbols {
			if !classKind(sym.Kind) {
				continue
			}
			if _, seen := loc[sym.Name]; seen {
				continue
			}
			loc[sym.Name] = typeLoc{pf.FileInfo.Path, sym.StartLine, sym.EndLine}
		}
	}

	depthOf := makeDITResolver(parents)

	var out []Finding
	for child := range parents {
		dit := depthOf(child)
		if dit < t.DeepInheritanceDIT {
			continue
		}
		sev := SeverityMedium
		if t.DeepInheritanceDITHigh > 0 && dit >= t.DeepInheritanceDITHigh {
			sev = SeverityHigh
		}
		l := loc[child] // zero value (empty path) when the type isn't in the parsed set
		out = append(out, Finding{
			FilePath:      l.path,
			BiomarkerType: BiomarkerDeepInheritance,
			Severity:      sev,
			FunctionName:  child,
			LineStart:     l.startLine,
			LineEnd:       l.endLine,
			HealthImpact:  deepInheritanceImpact(dit, t),
			Reason:        fmt.Sprintf("%q sits %d levels deep in its inheritance chain", child, dit),
			Details:       map[string]any{"depth": dit},
		})
	}
	return out
}

// makeDITResolver returns a memoized function computing the depth of inheritance
// tree for a type: the number of edges on the longest chain from the type up to
// a root. A type with no recorded parent has depth 0. The in-progress set breaks
// cycles (malformed heritage) so resolution always terminates.
func makeDITResolver(parents map[string][]string) func(string) int {
	memo := map[string]int{}
	inProgress := map[string]bool{}
	var depth func(string) int
	depth = func(name string) int {
		if d, ok := memo[name]; ok {
			return d
		}
		ps := parents[name]
		if len(ps) == 0 {
			memo[name] = 0
			return 0
		}
		if inProgress[name] {
			return 0 // cycle: stop descending, don't credit extra depth
		}
		inProgress[name] = true
		best := 0
		for _, p := range ps {
			if d := depth(p); d > best {
				best = d
			}
		}
		inProgress[name] = false
		memo[name] = best + 1
		return best + 1
	}
	return depth
}

func deepInheritanceImpact(dit int, t Thresholds) float64 {
	if dit < t.DeepInheritanceDIT {
		return 0
	}
	if t.DeepInheritanceDITHigh <= t.DeepInheritanceDIT || dit >= t.DeepInheritanceDITHigh {
		return 2.0
	}
	span := float64(t.DeepInheritanceDITHigh - t.DeepInheritanceDIT)
	progress := float64(dit-t.DeepInheritanceDIT) / span
	return 1.0 + progress
}
