// Package providerfactory builds LLM providers and embedders from a resolved
// config.Config. It lives in its own neutral package (rather than alongside a
// CLI command) so every caller that has a Config — the CLI commands, the HTTP
// server, and the auto-reindex watcher's post-pipeline task runner — can
// construct a provider the same way without importing cli/commands.
package providerfactory

import (
	"fmt"
	"os"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/providers"
)

// firstSet returns the first non-empty string, or "".
func firstSet(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Optional returns the configured provider and model, or (nil, "") when no
// real provider is configured (provider unset or "mock"). Callers degrade
// gracefully to no-LLM behaviour when the provider is nil.
func Optional(cfg config.Config) (providers.Provider, string) {
	if cfg.Provider == "" || cfg.Provider == "mock" {
		return nil, ""
	}
	prov, err := Build(cfg.Provider, cfg.Model, cfg)
	if err != nil {
		return nil, ""
	}
	return prov, cfg.Model
}

// Build hydrates a Provider with config-derived options. Returns an error when
// a required API key or base URL is missing for the named provider.
func Build(name, model string, cfg config.Config) (providers.Provider, error) {
	opts := providers.Options{}
	switch name {
	case "anthropic":
		key := cfg.ProviderKeys.Anthropic
		if key == "" {
			key = os.Getenv("ANTHROPIC_API_KEY")
		}
		if key == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY is required for the anthropic provider")
		}
		opts["api_key"] = key
		if model != "" {
			opts["default_model"] = model
		}
	case "openai":
		key := firstSet(cfg.ProviderKeys.OpenAI, os.Getenv("OPENAI_API_KEY"))
		if key == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is required for the openai provider")
		}
		opts["api_key"] = key
		if b := os.Getenv("VOR_OPENAI_BASE_URL"); b != "" {
			opts["base_url"] = b
		}
		if model != "" {
			opts["default_model"] = model
		}
	case "google":
		key := firstSet(cfg.ProviderKeys.Gemini, os.Getenv("GEMINI_API_KEY"), os.Getenv("GOOGLE_API_KEY"))
		if key == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY or GOOGLE_API_KEY is required for the google provider")
		}
		opts["api_key"] = key
		if model != "" {
			opts["default_model"] = model
		}
	case "ollama":
		// Local server — no key. Base URL overridable for remote hosts.
		if b := os.Getenv("VOR_OLLAMA_BASE_URL"); b != "" {
			opts["base_url"] = b
		}
		if model != "" {
			opts["default_model"] = model
		}
	case "litellm":
		base := os.Getenv("VOR_LITELLM_BASE_URL")
		if base == "" {
			return nil, fmt.Errorf("VOR_LITELLM_BASE_URL is required for the litellm provider")
		}
		opts["base_url"] = base
		if key := firstSet(cfg.ProviderKeys.OpenRouter, os.Getenv("LITELLM_API_KEY")); key != "" {
			opts["api_key"] = key
		}
		if model != "" {
			opts["default_model"] = model
		}
	case "mock":
		if model != "" {
			opts["model"] = model
		}
	}
	return providers.NewProvider(name, opts)
}

// OptionalEmbedder returns the configured embedder, or nil when none is set
// for real use (embedder unset or "mock"). Mirrors Optional for providers:
// callers that drive automatic work (e.g. the post-pipeline embedding task)
// use this so they no-op unless the user has configured a real embedder,
// rather than silently filling the index with mock vectors.
func OptionalEmbedder(cfg config.Config) providers.Embedder {
	if cfg.Embedder == "" || cfg.Embedder == "mock" {
		return nil
	}
	emb, err := Embedder(cfg)
	if err != nil {
		return nil
	}
	return emb
}

// Embedder constructs the configured embedder. Falls back to mock when unset so
// semantic features work zero-config.
func Embedder(cfg config.Config) (providers.Embedder, error) {
	name := cfg.Embedder
	if name == "" {
		name = "mock"
	}
	opts := providers.Options{}
	if cfg.EmbeddingModel != "" {
		opts["model"] = cfg.EmbeddingModel
	}
	if cfg.EmbeddingDims > 0 {
		opts["dimensions"] = cfg.EmbeddingDims
	}
	switch name {
	case "openai":
		if key := firstSet(cfg.ProviderKeys.OpenAI, os.Getenv("OPENAI_API_KEY")); key != "" {
			opts["api_key"] = key
		}
		if b := os.Getenv("VOR_OPENAI_BASE_URL"); b != "" {
			opts["base_url"] = b
		}
	case "google":
		if key := firstSet(cfg.ProviderKeys.Gemini, os.Getenv("GEMINI_API_KEY"), os.Getenv("GOOGLE_API_KEY")); key != "" {
			opts["api_key"] = key
		}
	case "ollama":
		if b := os.Getenv("VOR_OLLAMA_BASE_URL"); b != "" {
			opts["base_url"] = b
		}
	}
	return providers.NewEmbedder(name, opts)
}
