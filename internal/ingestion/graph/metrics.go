package graph

import (
	"gonum.org/v1/gonum/graph/network"
	"gonum.org/v1/gonum/graph/simple"
	"gonum.org/v1/gonum/graph/topo"
)

// ComputeMetrics fills in PageRank, in/out degree, and per-node strongly-
// connected-component IDs (stored as CommunityID for now) on every node.
//
// Betweenness centrality is computed lazily on demand because it is O(V*E)
// and we don't want it on every Build call.
//
// The Python implementation runs all three in `add_metrics`; we split them
// to mirror the cost profile rather than the API for now.
func (g *Graph) ComputeMetrics() {
	gonumG := g.gonumDirectedView()

	// Degree first — trivial.
	g.mu.Lock()
	for _, n := range g.byNumID {
		n.InDegree = 0
		n.OutDegree = 0
	}
	for _, e := range g.edges {
		e.F.OutDegree++
		e.T.InDegree++
	}
	g.mu.Unlock()

	// PageRank with the standard 0.85 damping factor and 1e-6 tolerance.
	ranks := network.PageRank(gonumG, 0.85, 1e-6)
	g.mu.Lock()
	for id, r := range ranks {
		if n, ok := g.byNumID[id]; ok {
			n.PageRank = r
		}
	}
	g.mu.Unlock()

	// Strongly-connected components, ID assigned by walk order.
	sccs := topo.TarjanSCC(gonumG)
	g.mu.Lock()
	for i, comp := range sccs {
		for _, member := range comp {
			if n, ok := g.byNumID[member.ID()]; ok {
				n.CommunityID = i + 1 // 0 reserved for "no community"
			}
		}
	}
	g.mu.Unlock()
}

// ComputeBetweenness fills in Node.Betweenness using gonum's brandes
// algorithm. Separate from ComputeMetrics so callers opt in.
func (g *Graph) ComputeBetweenness() {
	gonumG := g.gonumDirectedView()
	scores := network.Betweenness(gonumG)
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, s := range scores {
		if n, ok := g.byNumID[id]; ok {
			n.Betweenness = s
		}
	}
}

// gonumDirectedView mirrors the typed multigraph into a simple.DirectedGraph
// for use by gonum algorithms. Multi-edges between the same pair collapse
// to a single edge (acceptable for PageRank / SCC / betweenness, all of
// which treat the graph as a binary adjacency relation).
func (g *Graph) gonumDirectedView() *simple.DirectedGraph {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := simple.NewDirectedGraph()
	for _, n := range g.byNumID {
		out.AddNode(simple.Node(n.numID))
	}
	for _, e := range g.edges {
		if e.F.numID == e.T.numID {
			// gonum/simple disallows self-loops; skip silently.
			continue
		}
		if out.HasEdgeFromTo(e.F.numID, e.T.numID) {
			continue
		}
		out.SetEdge(simple.Edge{F: simple.Node(e.F.numID), T: simple.Node(e.T.numID)})
	}
	return out
}
