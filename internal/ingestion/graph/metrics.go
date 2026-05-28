package graph

import (
	"math/rand/v2"
	"sort"

	"gonum.org/v1/gonum/graph"
	"gonum.org/v1/gonum/graph/community"
	"gonum.org/v1/gonum/graph/network"
	"gonum.org/v1/gonum/graph/simple"
)

// communityResolution is the Louvain resolution parameter. 1.0 is the
// standard modularity setting; higher values yield more, smaller
// communities. Kept at 1.0 to match the reference behaviour.
const communityResolution = 1.0

// ComputeMetrics fills in PageRank, in/out degree, and per-node community
// IDs on every node.
//
// Communities come from Louvain modularity maximisation (gonum) over an
// undirected weighted view of the graph — replacing the earlier
// connected-component placeholder, which lumped every weakly-related file
// into one giant component. Betweenness centrality is computed lazily on
// demand because it is O(V*E).
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

	g.computeCommunities()
}

// computeCommunities assigns CommunityID via Louvain over an undirected
// weighted view. The result is deterministic: communities are sorted by
// (size desc, smallest member id) before IDs are handed out, and the
// Louvain source is fixed-seeded.
func (g *Graph) computeCommunities() {
	uview := g.gonumUndirectedView()
	// math/rand/v2 PCG with a fixed seed keeps community labelling stable
	// across runs for the same input graph.
	reduced := community.Modularize(uview, communityResolution, rand.NewPCG(1, 1))
	comms := reduced.Communities()

	// Deterministic ordering: largest communities first, ties broken by the
	// smallest node id they contain.
	sort.SliceStable(comms, func(i, j int) bool {
		if len(comms[i]) != len(comms[j]) {
			return len(comms[i]) > len(comms[j])
		}
		return minNodeID(comms[i]) < minNodeID(comms[j])
	})

	g.mu.Lock()
	defer g.mu.Unlock()
	for i, comp := range comms {
		for _, member := range comp {
			if n, ok := g.byNumID[member.ID()]; ok {
				n.CommunityID = i + 1 // 0 reserved for "no community"
			}
		}
	}
}

func minNodeID(nodes []graph.Node) int64 {
	min := int64(1<<63 - 1)
	for _, n := range nodes {
		if n.ID() < min {
			min = n.ID()
		}
	}
	return min
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

// gonumUndirectedView mirrors the typed multigraph into an undirected
// weighted graph for Louvain. Parallel edges between the same pair (and
// both directions of a directed edge) accumulate as edge weight, so
// densely-connected file/symbol pairs pull into the same community.
func (g *Graph) gonumUndirectedView() *simple.WeightedUndirectedGraph {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := simple.NewWeightedUndirectedGraph(0, 0)
	for _, n := range g.byNumID {
		out.AddNode(simple.Node(n.numID))
	}
	for _, e := range g.edges {
		if e.F.numID == e.T.numID {
			continue // no self-loops
		}
		w := 1.0
		if out.HasEdgeBetween(e.F.numID, e.T.numID) {
			w = out.WeightedEdgeBetween(e.F.numID, e.T.numID).Weight() + 1.0
		}
		out.SetWeightedEdge(simple.WeightedEdge{
			F: simple.Node(e.F.numID), T: simple.Node(e.T.numID), W: w,
		})
	}
	return out
}
