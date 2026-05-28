package graph

import (
	"math"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// Build a tiny diamond graph by hand: a -> b, a -> c, b -> d, c -> d.
// PageRank should rank d highest (sink), then b == c, then a.
func TestComputeMetrics_Diamond(t *testing.T) {
	g := New()
	a := g.AddNode(&Node{StringID: "a", Kind: NodeFile})
	b := g.AddNode(&Node{StringID: "b", Kind: NodeFile})
	c := g.AddNode(&Node{StringID: "c", Kind: NodeFile})
	d := g.AddNode(&Node{StringID: "d", Kind: NodeFile})
	g.AddEdge(a, b, models.EdgeImports, 1.0, nil)
	g.AddEdge(a, c, models.EdgeImports, 1.0, nil)
	g.AddEdge(b, d, models.EdgeImports, 1.0, nil)
	g.AddEdge(c, d, models.EdgeImports, 1.0, nil)

	g.ComputeMetrics()

	if a.OutDegree != 2 {
		t.Errorf("a.OutDegree = %d, want 2", a.OutDegree)
	}
	if d.InDegree != 2 {
		t.Errorf("d.InDegree = %d, want 2", d.InDegree)
	}
	if a.InDegree != 0 {
		t.Errorf("a.InDegree = %d, want 0", a.InDegree)
	}

	// Each rank must be in (0,1); they should sum to ~1.
	total := a.PageRank + b.PageRank + c.PageRank + d.PageRank
	if math.Abs(total-1.0) > 0.01 {
		t.Errorf("PageRank sum = %v, want ~1.0", total)
	}
	if d.PageRank <= b.PageRank || d.PageRank <= c.PageRank {
		t.Errorf("d should outrank b and c: d=%v b=%v c=%v", d.PageRank, b.PageRank, c.PageRank)
	}
}

func TestComputeMetrics_DisconnectedComponentsSeparate(t *testing.T) {
	// Two separate cycles: {a,b,c} and {d,e}.
	g := New()
	a := g.AddNode(&Node{StringID: "a", Kind: NodeFile})
	b := g.AddNode(&Node{StringID: "b", Kind: NodeFile})
	c := g.AddNode(&Node{StringID: "c", Kind: NodeFile})
	d := g.AddNode(&Node{StringID: "d", Kind: NodeFile})
	e := g.AddNode(&Node{StringID: "e", Kind: NodeFile})
	g.AddEdge(a, b, models.EdgeCalls, 1.0, nil)
	g.AddEdge(b, c, models.EdgeCalls, 1.0, nil)
	g.AddEdge(c, a, models.EdgeCalls, 1.0, nil)
	g.AddEdge(d, e, models.EdgeCalls, 1.0, nil)
	g.AddEdge(e, d, models.EdgeCalls, 1.0, nil)

	g.ComputeMetrics()

	// All three of a,b,c share a community; d,e share another.
	if a.CommunityID != b.CommunityID || b.CommunityID != c.CommunityID {
		t.Errorf("a/b/c community IDs differ: %d/%d/%d",
			a.CommunityID, b.CommunityID, c.CommunityID)
	}
	if d.CommunityID != e.CommunityID {
		t.Errorf("d/e community IDs differ: %d/%d", d.CommunityID, e.CommunityID)
	}
	if a.CommunityID == d.CommunityID {
		t.Errorf("separate cycles got the same community ID: %d", a.CommunityID)
	}
}

func TestComputeMetrics_LouvainSplitsBridgedClusters(t *testing.T) {
	// Two dense triangles joined by a single bridge edge. A connected-
	// component labeller would lump all six nodes into one community;
	// Louvain modularity should keep the two clusters apart.
	g := New()
	mk := func(id string) *Node { return g.AddNode(&Node{StringID: id, Kind: NodeFile}) }
	a1, a2, a3 := mk("a1"), mk("a2"), mk("a3")
	b1, b2, b3 := mk("b1"), mk("b2"), mk("b3")
	dense := func(x, y, z *Node) {
		g.AddEdge(x, y, models.EdgeCalls, 1.0, nil)
		g.AddEdge(y, z, models.EdgeCalls, 1.0, nil)
		g.AddEdge(z, x, models.EdgeCalls, 1.0, nil)
	}
	dense(a1, a2, a3)
	dense(b1, b2, b3)
	g.AddEdge(a1, b1, models.EdgeImports, 1.0, nil) // single bridge

	g.ComputeMetrics()

	if a1.CommunityID != a2.CommunityID || a2.CommunityID != a3.CommunityID {
		t.Errorf("cluster A split across communities: %d/%d/%d", a1.CommunityID, a2.CommunityID, a3.CommunityID)
	}
	if b1.CommunityID != b2.CommunityID || b2.CommunityID != b3.CommunityID {
		t.Errorf("cluster B split across communities: %d/%d/%d", b1.CommunityID, b2.CommunityID, b3.CommunityID)
	}
	if a1.CommunityID == b1.CommunityID {
		t.Errorf("bridged clusters collapsed into one community %d — Louvain should separate them", a1.CommunityID)
	}
}

func TestComputeMetrics_HandlesSelfLoops(t *testing.T) {
	// simple.DirectedGraph disallows self-loops; the metrics view must
	// skip them silently rather than panic.
	g := New()
	a := g.AddNode(&Node{StringID: "a", Kind: NodeFile})
	g.AddEdge(a, a, models.EdgeCalls, 1.0, nil) // self-loop

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ComputeMetrics panicked on self-loop: %v", r)
		}
	}()
	g.ComputeMetrics()
}

func TestComputeBetweenness(t *testing.T) {
	// a -> b -> c, so b is on the only path from a to c; betweenness(b)>0.
	g := New()
	a := g.AddNode(&Node{StringID: "a", Kind: NodeFile})
	b := g.AddNode(&Node{StringID: "b", Kind: NodeFile})
	c := g.AddNode(&Node{StringID: "c", Kind: NodeFile})
	g.AddEdge(a, b, models.EdgeCalls, 1.0, nil)
	g.AddEdge(b, c, models.EdgeCalls, 1.0, nil)

	g.ComputeBetweenness()
	if b.Betweenness <= 0 {
		t.Errorf("Betweenness(b) = %v, want > 0", b.Betweenness)
	}
	if a.Betweenness != 0 {
		t.Errorf("Betweenness(a) = %v, want 0", a.Betweenness)
	}
}
