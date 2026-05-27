package mock_test

import (
	"context"
	"strings"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/providers"
	_ "github.com/repowise-dev/repowise-go/internal/providers/mock"
)

func newMockProvider(t *testing.T) providers.Provider {
	t.Helper()
	p, err := providers.NewProvider("mock", nil)
	if err != nil {
		t.Fatalf("NewProvider(mock): %v", err)
	}
	return p
}

func TestMock_GenerateIsDeterministic(t *testing.T) {
	p := newMockProvider(t)
	req := providers.Request{
		Model:    "mock-1",
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hello"}},
	}
	r1, err := p.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	r2, err := p.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate (2nd): %v", err)
	}
	if r1.Content != r2.Content {
		t.Errorf("mock output not deterministic:\n  r1=%q\n  r2=%q", r1.Content, r2.Content)
	}
	if !strings.Contains(r1.Content, "hello") {
		t.Errorf("mock output should include input echo, got %q", r1.Content)
	}
	if r1.Usage.InputTokens == 0 || r1.Usage.OutputTokens == 0 {
		t.Errorf("mock usage should be non-zero, got %+v", r1.Usage)
	}
}

func TestMock_GenerateStream(t *testing.T) {
	p := newMockProvider(t)
	ch, err := p.GenerateStream(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "stream me"}},
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	var assembled strings.Builder
	var sawUsage bool
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("stream err: %v", ev.Err)
		}
		assembled.WriteString(ev.TextDelta)
		if ev.Usage != nil {
			sawUsage = true
		}
	}
	if assembled.Len() == 0 {
		t.Errorf("no text deltas received")
	}
	if !sawUsage {
		t.Errorf("no terminating Usage event received")
	}
}

func TestMock_GenerateRespectsContext(t *testing.T) {
	p := newMockProvider(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	_, err := p.Generate(ctx, providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Errorf("expected ctx.Err() when context already cancelled")
	}
}

func TestMockEmbedder_DeterministicAndSizedRight(t *testing.T) {
	e, err := providers.NewEmbedder("mock", providers.Options{"dimensions": 16})
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	if e.Dimensions() != 16 {
		t.Errorf("Dimensions = %d, want 16", e.Dimensions())
	}
	vecs1, err := e.Embed(context.Background(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	vecs2, _ := e.Embed(context.Background(), []string{"alpha", "beta"})
	if len(vecs1) != 2 || len(vecs1[0]) != 16 {
		t.Errorf("vec shape = (%d, %d), want (2, 16)", len(vecs1), len(vecs1[0]))
	}
	if !eqFloat32(vecs1[0], vecs2[0]) || !eqFloat32(vecs1[1], vecs2[1]) {
		t.Errorf("mock embedder not deterministic")
	}
	if eqFloat32(vecs1[0], vecs1[1]) {
		t.Errorf("different inputs produced identical vectors")
	}
}

func eqFloat32(a, b []float32) bool {
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
