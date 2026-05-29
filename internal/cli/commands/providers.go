package commands

import (
	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/providerfactory"
	"github.com/HundredAcreStudio/vor/internal/providers"
)

// Provider/embedder construction shared by the commands that need an LLM or
// embedder (serve, mcp, search, generate). The actual config→provider mapping
// lives in internal/providerfactory so non-CLI callers (the HTTP server, the
// post-pipeline task runner) can build providers without importing this
// package. These thin wrappers keep the existing command call sites stable.

func buildEmbedder(cfg config.Config) (providers.Embedder, error) {
	return providerfactory.Embedder(cfg)
}

func buildOptionalProvider(cfg config.Config) (providers.Provider, string) {
	return providerfactory.Optional(cfg)
}

func buildProvider(name, model string, cfg config.Config) (providers.Provider, error) {
	return providerfactory.Build(name, model, cfg)
}
