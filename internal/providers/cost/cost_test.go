package cost_test

import (
	"math"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/providers"
	"github.com/repowise-dev/repowise-go/internal/providers/cost"
)

func TestCatalog_PrefixMatch(t *testing.T) {
	p := cost.Catalog("anthropic", "claude-3-5-sonnet-20241022")
	if p.InputPerMillion == 0 {
		t.Errorf("expected non-zero pricing for claude-3-5-sonnet variant; got %+v", p)
	}
}

// Real model ids use dotted version segments (gemini-2.0-flash,
// gpt-4o-mini); the catalog keys must prefix-match those exactly.
func TestCatalog_RealModelIDsResolve(t *testing.T) {
	cases := []struct{ provider, model string }{
		{"openai", "gpt-4o-2024-08-06"},
		{"openai", "gpt-4o-mini"},
		{"openai", "gpt-4.1-mini"},
		{"google", "gemini-2.0-flash"},
		{"google", "gemini-1.5-pro-latest"},
		{"google", "gemini-1.5-flash"},
	}
	for _, c := range cases {
		p := cost.Catalog(c.provider, c.model)
		if p.InputPerMillion == 0 && p.OutputPerMillion == 0 {
			t.Errorf("%s/%s should resolve to non-zero pricing; got %+v", c.provider, c.model, p)
		}
	}
	// gpt-4o-mini must win the longest-prefix match over gpt-4o.
	mini := cost.Catalog("openai", "gpt-4o-mini")
	full := cost.Catalog("openai", "gpt-4o")
	if mini == full {
		t.Errorf("gpt-4o-mini should price differently from gpt-4o (longest-prefix): %+v", mini)
	}
}

func TestCatalog_UnknownReturnsZero(t *testing.T) {
	p := cost.Catalog("nope", "anything")
	if p.InputPerMillion != 0 || p.OutputPerMillion != 0 {
		t.Errorf("unknown provider should return zero pricing; got %+v", p)
	}
}

func TestCatalog_CaseInsensitiveProvider(t *testing.T) {
	a := cost.Catalog("Anthropic", "claude-3-5-sonnet")
	b := cost.Catalog("anthropic", "claude-3-5-sonnet")
	if a != b {
		t.Errorf("provider lookup should be case-insensitive: %v vs %v", a, b)
	}
}

func TestEstimate_Multiplies(t *testing.T) {
	p := cost.Pricing{InputPerMillion: 3, OutputPerMillion: 15, CachedReadPerMillion: 0.3}
	usage := providers.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CachedTokens: 1_000_000}
	got := cost.Estimate(usage, p)
	want := 3.0 + 15.0 + 0.3
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("Estimate = %v, want %v", got, want)
	}
}

func TestEstimate_ScalesProportionally(t *testing.T) {
	p := cost.Pricing{InputPerMillion: 3, OutputPerMillion: 15}
	usage := providers.Usage{InputTokens: 250_000, OutputTokens: 500_000}
	// 0.25 * 3 + 0.5 * 15 = 0.75 + 7.5 = 8.25
	got := cost.Estimate(usage, p)
	want := 8.25
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("Estimate = %v, want %v", got, want)
	}
}

func TestEstimateFor_Convenience(t *testing.T) {
	got := cost.EstimateFor("anthropic", "claude-3-5-sonnet",
		providers.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000})
	// $3 in + $15 out = $18 per million each.
	if math.Abs(got-18.0) > 1e-9 {
		t.Errorf("EstimateFor = %v, want 18", got)
	}
}

func TestMockProviderIsFree(t *testing.T) {
	got := cost.EstimateFor("mock", "mock-1",
		providers.Usage{InputTokens: 1_000_000_000, OutputTokens: 1_000_000_000})
	if got != 0 {
		t.Errorf("mock provider should be free, got %v", got)
	}
}
