package commands

// Side-effect imports that register the LLM provider and embedder vendors.
// Without these the registry is empty in the running binary, so
// providers.NewProvider / NewEmbedder fail and every provider-backed feature
// (wiki generation, embeddings, MCP synthesis) silently degrades to no-LLM.
// Importing them here — the package every subcommand lives in — registers all
// vendors (and their model catalogs) once for the whole vor binary.
import (
	_ "github.com/HundredAcreStudio/vor/internal/providers/anthropic"
	_ "github.com/HundredAcreStudio/vor/internal/providers/google"
	_ "github.com/HundredAcreStudio/vor/internal/providers/litellm"
	_ "github.com/HundredAcreStudio/vor/internal/providers/mock"
	_ "github.com/HundredAcreStudio/vor/internal/providers/ollama"
	_ "github.com/HundredAcreStudio/vor/internal/providers/openai"
)
