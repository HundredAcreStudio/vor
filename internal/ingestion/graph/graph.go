// Package graph holds the in-memory dependency graph produced by ingestion.
// File nodes and symbol nodes are linked by typed edges (imports, calls,
// defines, has_method, ...). gonum is used for graph algorithms (PageRank,
// SCC, betweenness) via a thin adapter; the canonical store is our own
// typed slice/map so multi-edges (e.g. file→file imports vs file→file
// type_use between the same pair) can coexist.
//
// Mirrors the Python `core.ingestion.graph.GraphBuilder` shape: Build is a
// two-phase operation — AddFile() per parsed file, then Build() to resolve
// imports/calls into edges.
package graph

import (
	"sync"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// NodeKind classifies a graph node.
type NodeKind string

const (
	NodeFile   NodeKind = "file"
	NodeSymbol NodeKind = "symbol"
)

// Node is the metadata stored for each graph node. ID() is the gonum-side
// integer identifier, but downstream consumers should prefer StringID,
// which matches the persistence layer's graph_nodes.node_id column.
type Node struct {
	numID int64

	// StringID is the canonical identity: a relative file path
	// ("src/foo.go") for NodeFile, or "<relpath>::<symbol>" for NodeSymbol.
	StringID string
	Kind     NodeKind

	// Path is the repo-relative file path.
	Path string

	Language models.LanguageTag

	// Symbol is populated for NodeSymbol; nil for NodeFile.
	Symbol *models.Symbol

	// Metrics — populated by ComputeMetrics, zero before.
	PageRank    float64
	Betweenness float64
	CommunityID int
	InDegree    int
	OutDegree   int

	// HasError flips true when the parser reported a non-fatal error for
	// the file containing this node. Mirrors graph_nodes.has_error.
	HasError bool

	// IsTest, IsEntryPoint copied from FileInfo for fast graph-side filters.
	IsTest       bool
	IsEntryPoint bool

	// SymbolCount is only meaningful on file nodes — the number of symbols
	// defined in the file.
	SymbolCount int
}

// ID satisfies gonum.graph.Node.
func (n *Node) ID() int64 { return n.numID }

// Edge is a typed directed edge between two nodes.
type Edge struct {
	F, T          *Node
	Type          models.EdgeType
	Confidence    float64
	ImportedNames []string
}

// Graph is the repowise dependency graph for one repository.
type Graph struct {
	mu        sync.RWMutex
	byString  map[string]*Node
	byNumID   map[int64]*Node
	edges     []*Edge // canonical edge store; iterate to derive metrics
	nextNumID int64

	// outgoing / incoming adjacency lists keyed by Node.StringID. Edge
	// objects are shared with the `edges` slice (pointer aliases).
	outgoing map[string][]*Edge
	incoming map[string][]*Edge
}

// New returns an empty Graph.
func New() *Graph {
	return &Graph{
		byString: map[string]*Node{},
		byNumID:  map[int64]*Node{},
		outgoing: map[string][]*Edge{},
		incoming: map[string][]*Edge{},
	}
}

// AddNode inserts a node by StringID. If a node with the same StringID
// already exists the existing node is returned (and the input is ignored).
// Callers should prefer the type-specific helpers (AddFileNode /
// AddSymbolNode) which fill in metadata.
func (g *Graph) AddNode(n *Node) *Node {
	g.mu.Lock()
	defer g.mu.Unlock()
	if existing, ok := g.byString[n.StringID]; ok {
		return existing
	}
	n.numID = g.nextNumID
	g.nextNumID++
	g.byString[n.StringID] = n
	g.byNumID[n.numID] = n
	return n
}

// AddFileNode adds (or returns the existing) file node for fi.
func (g *Graph) AddFileNode(fi models.FileInfo) *Node {
	return g.AddNode(&Node{
		StringID:     fi.Path,
		Kind:         NodeFile,
		Path:         fi.Path,
		Language:     fi.Language,
		IsTest:       fi.IsTest,
		IsEntryPoint: fi.IsEntryPoint,
	})
}

// AddSymbolNode adds (or returns) a symbol node for sym extracted from
// filePath. The symbol's Symbol.ID becomes the node's StringID.
func (g *Graph) AddSymbolNode(filePath string, sym models.Symbol) *Node {
	s := sym
	return g.AddNode(&Node{
		StringID: sym.ID,
		Kind:     NodeSymbol,
		Path:     filePath,
		Language: models.LanguageTag(sym.Language),
		Symbol:   &s,
	})
}

// AddEdge inserts an edge from f to t of the given type. If an edge with
// the same (from, to, type) already exists it is updated (confidence
// max-merged, imported names unioned).
func (g *Graph) AddEdge(f, t *Node, kind models.EdgeType, confidence float64, importedNames []string) *Edge {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.outgoing[f.StringID] {
		if e.T == t && e.Type == kind {
			if confidence > e.Confidence {
				e.Confidence = confidence
			}
			e.ImportedNames = unionStrings(e.ImportedNames, importedNames)
			return e
		}
	}
	e := &Edge{
		F: f, T: t, Type: kind,
		Confidence:    confidence,
		ImportedNames: append([]string(nil), importedNames...),
	}
	g.edges = append(g.edges, e)
	g.outgoing[f.StringID] = append(g.outgoing[f.StringID], e)
	g.incoming[t.StringID] = append(g.incoming[t.StringID], e)
	return e
}

// LookupNode returns the node with the given StringID, or nil.
func (g *Graph) LookupNode(stringID string) *Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.byString[stringID]
}

// Nodes returns all nodes in insertion order. Snapshot — safe to iterate
// without holding the graph lock.
func (g *Graph) Nodes() []*Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]*Node, 0, len(g.byString))
	// Stable iteration order by numID.
	for i := int64(0); i < g.nextNumID; i++ {
		if n, ok := g.byNumID[i]; ok {
			out = append(out, n)
		}
	}
	return out
}

// Edges returns all edges in insertion order. Snapshot.
func (g *Graph) Edges() []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]*Edge(nil), g.edges...)
}

// Outgoing returns edges starting at stringID. Snapshot.
func (g *Graph) Outgoing(stringID string) []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]*Edge(nil), g.outgoing[stringID]...)
}

// Incoming returns edges ending at stringID. Snapshot.
func (g *Graph) Incoming(stringID string) []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]*Edge(nil), g.incoming[stringID]...)
}

// NodeCount returns the total number of nodes.
func (g *Graph) NodeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.byString)
}

// EdgeCount returns the total number of edges (typed multi-edges counted
// individually).
func (g *Graph) EdgeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.edges)
}

// CountByEdgeType returns a per-type edge tally; useful for stats and tests.
func (g *Graph) CountByEdgeType() map[models.EdgeType]int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := map[models.EdgeType]int{}
	for _, e := range g.edges {
		out[e.Type]++
	}
	return out
}

// unionStrings returns a unique-merged slice preserving the order of a then b.
func unionStrings(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range b {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
