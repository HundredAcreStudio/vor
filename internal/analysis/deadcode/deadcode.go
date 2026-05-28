// Package deadcode finds files and symbols in the dependency graph that
// no live entry point can reach. Mirrors the Python
// `core.analysis.dead_code.UnreachableCodeDetector`.
//
// The algorithm is a marking sweep over a graph already built by
// internal/ingestion/graph. Roots come from two sources:
//
//  1. File nodes flagged IsEntryPoint by the traverser (main.py, index.ts,
//     main.go, etc.).
//  2. Symbol nodes inside entry-point files.
//
// We BFS from roots following EdgeImports + EdgeDefines + EdgeCalls +
// EdgeHasMethod + EdgeHasProperty. Anything not visited is dead. The
// finding's Confidence reflects how confident we are that the node is
// truly dead vs maybe-called-dynamically:
//
//   - 1.0  unreachable file with zero in-edges (no one ever imports it)
//   - 0.8  unreachable symbol with zero in-edges
//   - 0.5  unreachable symbol that has some in-edges (those edges point at
//     dead callers too, so it's circular dead code — still likely dead)
//
// Confidence thresholds live on the Analyzer so callers can tune.
package deadcode

import (
	"github.com/repowise-dev/repowise-go/internal/ingestion/graph"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// Kind classifies a finding.
type Kind string

const (
	KindUnreachableFile   Kind = "unreachable_file"
	KindUnreachableSymbol Kind = "unreachable_symbol"
)

// Finding is one dead-code result. Persisted directly into
// dead_code_findings.
type Finding struct {
	Kind         Kind
	FilePath     string
	SymbolName   string // empty for unreachable_file
	SymbolKind   string // empty for unreachable_file
	Confidence   float64
	Reason       string
	SafeToDelete bool
	StartLine    int // 1-indexed
	EndLine      int
}

// Analyzer turns a graph into a list of findings.
type Analyzer struct {
	// MinConfidence filters out findings below this confidence.
	// Default 0.0 (keep everything).
	MinConfidence float64

	// IncludeTestFiles, when false, skips files marked IsTest in the
	// finding set — test-only code is rarely actionable dead code.
	IncludeTestFiles bool

	// ExtraRoots is an optional list of node StringIDs to treat as live
	// roots in addition to the entry-point heuristic. Use for symbols
	// known to be invoked dynamically (e.g. handlers registered at
	// runtime).
	ExtraRoots []string
}

// Analyze runs the dead-code sweep over g and returns findings sorted by
// confidence descending, then file path.
func (a *Analyzer) Analyze(g *graph.Graph) []Finding {
	live := liveSet(g, a.ExtraRoots)
	findings := make([]Finding, 0)

	for _, n := range g.Nodes() {
		if _, alive := live[n.StringID]; alive {
			continue
		}

		switch n.Kind {
		case graph.NodeFile:
			if !a.IncludeTestFiles && n.IsTest {
				continue
			}
			if n.IsEntryPoint {
				// Entry points should never be flagged.
				continue
			}
			confidence, reason := classifyFile(g, n)
			if confidence < a.MinConfidence {
				continue
			}
			findings = append(findings, Finding{
				Kind:         KindUnreachableFile,
				FilePath:     n.Path,
				Confidence:   confidence,
				Reason:       reason,
				SafeToDelete: confidence >= 0.9,
			})

		case graph.NodeSymbol:
			if !a.IncludeTestFiles && symbolInTestFile(g, n) {
				continue
			}
			confidence, reason := classifySymbol(g, n, live)
			if confidence < a.MinConfidence {
				continue
			}
			f := Finding{
				Kind:         KindUnreachableSymbol,
				FilePath:     n.Path,
				Confidence:   confidence,
				Reason:       reason,
				SafeToDelete: confidence >= 0.9,
			}
			if n.Symbol != nil {
				f.SymbolName = n.Symbol.Name
				f.SymbolKind = string(n.Symbol.Kind)
				f.StartLine = n.Symbol.StartLine
				f.EndLine = n.Symbol.EndLine
			}
			findings = append(findings, f)
		}
	}

	sortFindings(findings)
	return findings
}

// liveSet returns the set of node StringIDs reachable from entry-point
// files (plus any caller-supplied extra roots) via the live-edge types.
func liveSet(g *graph.Graph, extraRoots []string) map[string]struct{} {
	live := map[string]struct{}{}
	queue := make([]string, 0, 64)

	for _, n := range g.Nodes() {
		if n.Kind == graph.NodeFile && n.IsEntryPoint {
			live[n.StringID] = struct{}{}
			queue = append(queue, n.StringID)
		}
	}
	for _, root := range extraRoots {
		if n := g.LookupNode(root); n != nil {
			if _, ok := live[root]; !ok {
				live[root] = struct{}{}
				queue = append(queue, root)
			}
		}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.Outgoing(cur) {
			if !isLiveEdge(e.Type) {
				continue
			}
			if _, seen := live[e.T.StringID]; seen {
				continue
			}
			live[e.T.StringID] = struct{}{}
			queue = append(queue, e.T.StringID)
		}
	}
	return live
}

func isLiveEdge(t models.EdgeType) bool {
	switch t {
	case models.EdgeImports,
		models.EdgeDefines,
		models.EdgeCalls,
		models.EdgeHasMethod,
		models.EdgeHasProperty,
		models.EdgeExtends,
		models.EdgeImplements,
		models.EdgeMethodOverrides,
		models.EdgeMethodImplements,
		models.EdgeFramework,
		models.EdgeDynamic:
		return true
	default:
		return false
	}
}

// symbolInTestFile returns true when the symbol's containing file is
// flagged IsTest. The graph stores IsTest on the file node only; symbol
// nodes need to look it up.
func symbolInTestFile(g *graph.Graph, n *graph.Node) bool {
	if n == nil || n.Path == "" {
		return false
	}
	file := g.LookupNode(n.Path)
	return file != nil && file.IsTest
}

// classifyFile assigns confidence + reason to an unreachable file node.
func classifyFile(g *graph.Graph, n *graph.Node) (float64, string) {
	inbound := g.Incoming(n.StringID)
	if len(inbound) == 0 {
		return 1.0, "no incoming imports and not reachable from any entry point"
	}
	// Has some incoming edges but those edges come from other dead nodes
	// (we know it's dead because we already excluded live nodes above).
	return 0.6, "only referenced by other unreachable code"
}

// classifySymbol assigns confidence + reason to an unreachable symbol.
// (live is unused for now but kept as a parameter so future heuristics
// can reason about which inbound callers are themselves live.)
func classifySymbol(g *graph.Graph, n *graph.Node, _ map[string]struct{}) (float64, string) {
	inbound := g.Incoming(n.StringID)
	var nonDefIn int
	for _, e := range inbound {
		if e.Type == models.EdgeDefines {
			continue // defines = file→symbol; not a real call
		}
		nonDefIn++
	}
	if nonDefIn == 0 {
		return 0.8, "no callers or references"
	}
	return 0.5, "referenced only by unreachable callers"
}

// sortFindings stable-sorts by Confidence desc then FilePath.
func sortFindings(f []Finding) {
	// Tiny custom sort to avoid sort.Slice closure allocation for what is
	// typically a small slice.
	for i := 1; i < len(f); i++ {
		for j := i; j > 0; j-- {
			if findingLess(f[j], f[j-1]) {
				f[j], f[j-1] = f[j-1], f[j]
			} else {
				break
			}
		}
	}
}

func findingLess(a, b Finding) bool {
	if a.Confidence != b.Confidence {
		return a.Confidence > b.Confidence
	}
	if a.FilePath != b.FilePath {
		return a.FilePath < b.FilePath
	}
	return a.SymbolName < b.SymbolName
}
