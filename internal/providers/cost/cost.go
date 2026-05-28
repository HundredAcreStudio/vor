// Package cost computes the USD cost of a Provider.Response by looking up
// the per-million-token pricing for (provider, model). Prices are a
// best-effort snapshot — vendors update them periodically; the table below
// is intended to be obviously editable rather than authoritative.
package cost

import (
	"strings"

	"github.com/repowise-dev/repowise-go/internal/providers"
)

// Pricing is per-million-token USD prices for one model.
type Pricing struct {
	InputPerMillion      float64
	OutputPerMillion     float64
	CachedReadPerMillion float64 // Anthropic / OpenAI prompt-cache read rate
}

// Catalog returns the price for (provider, model), or the zero Pricing
// if unknown. Lookups are case-insensitive on provider; model is matched
// with longest-prefix to tolerate version-suffixed names like
// "claude-3-5-sonnet-20241022".
func Catalog(provider, model string) Pricing {
	provider = strings.ToLower(provider)
	model = strings.ToLower(model)
	prices, ok := table[provider]
	if !ok {
		return Pricing{}
	}
	// Longest-prefix match: "claude-3-5-sonnet-20241022" -> "claude-3-5-sonnet"
	var best string
	for prefix := range prices {
		if strings.HasPrefix(model, prefix) && len(prefix) > len(best) {
			best = prefix
		}
	}
	if best == "" {
		return Pricing{}
	}
	return prices[best]
}

// Estimate returns the dollar cost for a given Usage at the supplied
// pricing.
func Estimate(usage providers.Usage, p Pricing) float64 {
	cost := float64(usage.InputTokens)*p.InputPerMillion +
		float64(usage.OutputTokens)*p.OutputPerMillion +
		float64(usage.CachedTokens)*p.CachedReadPerMillion
	return cost / 1_000_000.0
}

// EstimateFor is the convenience over Catalog + Estimate.
func EstimateFor(provider, model string, usage providers.Usage) float64 {
	return Estimate(usage, Catalog(provider, model))
}

// Snapshot table. Values are USD per million tokens, sourced from public
// vendor pricing pages as of the port date. Keep the table flat so future
// updates are obvious — no nesting, no inheritance.
var table = map[string]map[string]Pricing{
	"anthropic": {
		"claude-opus-4":     {InputPerMillion: 15, OutputPerMillion: 75, CachedReadPerMillion: 1.5},
		"claude-sonnet-4":   {InputPerMillion: 3, OutputPerMillion: 15, CachedReadPerMillion: 0.3},
		"claude-haiku-4-5":  {InputPerMillion: 1, OutputPerMillion: 5, CachedReadPerMillion: 0.1},
		"claude-3-5-sonnet": {InputPerMillion: 3, OutputPerMillion: 15, CachedReadPerMillion: 0.3},
		"claude-3-5-haiku":  {InputPerMillion: 0.8, OutputPerMillion: 4, CachedReadPerMillion: 0.08},
		"claude-3-opus":     {InputPerMillion: 15, OutputPerMillion: 75, CachedReadPerMillion: 1.5},
	},
	"openai": {
		"gpt-4o":       {InputPerMillion: 2.5, OutputPerMillion: 10, CachedReadPerMillion: 1.25},
		"gpt-4o-mini":  {InputPerMillion: 0.15, OutputPerMillion: 0.6, CachedReadPerMillion: 0.075},
		"gpt-4.1":      {InputPerMillion: 2, OutputPerMillion: 8, CachedReadPerMillion: 0.5},
		"gpt-4.1-mini": {InputPerMillion: 0.4, OutputPerMillion: 1.6, CachedReadPerMillion: 0.1},
		"gpt-4-turbo":  {InputPerMillion: 10, OutputPerMillion: 30},
		"gpt-4":        {InputPerMillion: 30, OutputPerMillion: 60},
		"gpt-3.5":      {InputPerMillion: 0.5, OutputPerMillion: 1.5},
		"o1":           {InputPerMillion: 15, OutputPerMillion: 60},
		"o3-mini":      {InputPerMillion: 1.1, OutputPerMillion: 4.4},
		"o3":           {InputPerMillion: 2, OutputPerMillion: 8},
	},
	"google": {
		// Keys are prefixes of the lowercased model name; the dotted
		// version segments must match real model ids (e.g. gemini-2.0-flash).
		"gemini-2.5-pro":   {InputPerMillion: 1.25, OutputPerMillion: 10},
		"gemini-2.0-flash": {InputPerMillion: 0.1, OutputPerMillion: 0.4},
		"gemini-1.5-pro":   {InputPerMillion: 1.25, OutputPerMillion: 5},
		"gemini-1.5-flash": {InputPerMillion: 0.075, OutputPerMillion: 0.3},
		"gemini-1.5":       {InputPerMillion: 0.075, OutputPerMillion: 0.3},
	},
	"mock": {
		// Mock provider is free.
		"mock-": {InputPerMillion: 0, OutputPerMillion: 0},
	},
}
