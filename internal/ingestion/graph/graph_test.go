package graph

import (
	"testing"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

func TestAddNode_Dedupes(t *testing.T) {
	g := New()
	a := g.AddNode(&Node{StringID: "x", Kind: NodeFile})
	b := g.AddNode(&Node{StringID: "x", Kind: NodeFile})
	if a != b {
		t.Errorf("second AddNode of same StringID returned a new node")
	}
	if g.NodeCount() != 1 {
		t.Errorf("NodeCount = %d, want 1", g.NodeCount())
	}
}

func TestAddEdge_DedupesByFromToType(t *testing.T) {
	g := New()
	a := g.AddNode(&Node{StringID: "a", Kind: NodeFile})
	b := g.AddNode(&Node{StringID: "b", Kind: NodeFile})

	g.AddEdge(a, b, models.EdgeImports, 1.0, []string{"X"})
	g.AddEdge(a, b, models.EdgeImports, 0.5, []string{"Y"}) // dedupe + merge
	g.AddEdge(a, b, models.EdgeCalls, 0.7, nil)             // distinct type

	if g.EdgeCount() != 2 {
		t.Fatalf("EdgeCount = %d, want 2", g.EdgeCount())
	}
	edges := g.Edges()
	for _, e := range edges {
		if e.Type == models.EdgeImports {
			if e.Confidence != 1.0 {
				t.Errorf("imports edge confidence = %v, want 1.0 (max-merged)", e.Confidence)
			}
			if len(e.ImportedNames) != 2 {
				t.Errorf("imports edge names = %v, want [X Y]", e.ImportedNames)
			}
		}
	}
}

func TestAddEdge_MultiEdgeBetweenSamePair(t *testing.T) {
	g := New()
	a := g.AddNode(&Node{StringID: "a", Kind: NodeFile})
	b := g.AddNode(&Node{StringID: "b", Kind: NodeFile})

	g.AddEdge(a, b, models.EdgeImports, 1.0, nil)
	g.AddEdge(a, b, models.EdgeCalls, 0.8, nil)
	g.AddEdge(a, b, models.EdgeExtends, 0.9, nil)

	if g.EdgeCount() != 3 {
		t.Errorf("EdgeCount = %d, want 3", g.EdgeCount())
	}
	counts := g.CountByEdgeType()
	if counts[models.EdgeImports] != 1 || counts[models.EdgeCalls] != 1 || counts[models.EdgeExtends] != 1 {
		t.Errorf("per-type counts = %v", counts)
	}
}

func TestOutgoingIncoming(t *testing.T) {
	g := New()
	a := g.AddNode(&Node{StringID: "a", Kind: NodeFile})
	b := g.AddNode(&Node{StringID: "b", Kind: NodeFile})
	c := g.AddNode(&Node{StringID: "c", Kind: NodeFile})
	g.AddEdge(a, b, models.EdgeImports, 1.0, nil)
	g.AddEdge(a, c, models.EdgeImports, 1.0, nil)
	g.AddEdge(c, b, models.EdgeImports, 1.0, nil)

	if got := len(g.Outgoing("a")); got != 2 {
		t.Errorf("Outgoing(a) = %d, want 2", got)
	}
	if got := len(g.Incoming("b")); got != 2 {
		t.Errorf("Incoming(b) = %d, want 2", got)
	}
	if got := len(g.Outgoing("b")); got != 0 {
		t.Errorf("Outgoing(b) = %d, want 0", got)
	}
}

func TestUnionStrings(t *testing.T) {
	cases := []struct {
		a, b, want []string
	}{
		{nil, nil, nil},
		{[]string{"a"}, nil, []string{"a"}},
		{nil, []string{"b"}, []string{"b"}},
		{[]string{"a", "b"}, []string{"b", "c"}, []string{"a", "b", "c"}},
		{[]string{"a", "b"}, []string{"a", "b"}, []string{"a", "b"}},
	}
	for _, tc := range cases {
		got := unionStrings(tc.a, tc.b)
		if !eqStrings(got, tc.want) {
			t.Errorf("unionStrings(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
