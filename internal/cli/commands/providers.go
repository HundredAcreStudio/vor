package commands

import (
	"fmt"
	"os"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/providers"
)

// Provider/embedder construction shared by the commands that need an LLM or
// embedder (serve, mcp, search). These used to live alongside the generate
// and embed CLI commands; they were relocated here when those commands moved
// into the dashboard so the remaining callers keep compiling.

// firstSet returns the first non-empty string, or "".
func firstSet(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// buildEmbedder constructs the configured embedder. Falls back to mock when
// unset so semantic features work zero-config.
func buildEmbedder(cfg config.Config) (providers.Embedder, error) {
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

// buildOptionalProvider returns the configured provider, or (nil, "") when
// no real provider is configured (so callers degrade gracefully to no-LLM).
func buildOptionalProvider(cfg config.Config) (providers.Provider, string) {
	if cfg.Provider == "" || cfg.Provider == "mock" {
		return nil, ""
	}
	prov, err := buildProvider(cfg.Provider, cfg.Model, cfg)
	if err != nil {
		return nil, ""
	}
	return prov, cfg.Model
}

// buildProvider hydrates a Provider with config-derived options.
func buildProvider(name, model string, cfg config.Config) (providers.Provider, error) {
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
