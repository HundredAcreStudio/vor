package providers

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds provider factories. Per-vendor packages call Register in
// init() so the CLI / server can look up by name.
//
// Factories take an opaque options map (typically derived from env vars or
// config.yaml) so each vendor decides its own configuration shape — the
// generic provider package doesn't need to know about API keys, endpoints,
// or per-vendor toggles.
type providerFactory func(opts Options) (Provider, error)
type embedderFactory func(opts Options) (Embedder, error)

// Options is the bag passed to factory functions. Common keys:
//
//	"api_key"       string
//	"base_url"      string  (Ollama / litellm)
//	"model"         string  (for embedders that bind a model on construction)
//	"dimensions"    int     (embedders)
type Options map[string]any

var (
	registryMu sync.RWMutex
	providers  = map[string]providerFactory{}
	embedders  = map[string]embedderFactory{}
)

// RegisterProvider installs a provider factory under name.
func RegisterProvider(name string, fac func(opts Options) (Provider, error)) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := providers[name]; exists {
		panic(fmt.Sprintf("provider %q already registered", name))
	}
	providers[name] = fac
}

// RegisterEmbedder installs an embedder factory under name.
func RegisterEmbedder(name string, fac func(opts Options) (Embedder, error)) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := embedders[name]; exists {
		panic(fmt.Sprintf("embedder %q already registered", name))
	}
	embedders[name] = fac
}

// NewProvider constructs a Provider by name. Returns an error if no such
// factory is registered.
func NewProvider(name string, opts Options) (Provider, error) {
	registryMu.RLock()
	fac := providers[name]
	registryMu.RUnlock()
	if fac == nil {
		return nil, fmt.Errorf("provider %q not registered (known: %v)", name, RegisteredProviders())
	}
	return fac(opts)
}

// NewEmbedder constructs an Embedder by name.
func NewEmbedder(name string, opts Options) (Embedder, error) {
	registryMu.RLock()
	fac := embedders[name]
	registryMu.RUnlock()
	if fac == nil {
		return nil, fmt.Errorf("embedder %q not registered (known: %v)", name, RegisteredEmbedders())
	}
	return fac(opts)
}

// RegisteredProviders returns the alphabetised list of provider names.
func RegisteredProviders() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(providers))
	for k := range providers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RegisteredEmbedders returns the alphabetised list of embedder names.
func RegisteredEmbedders() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(embedders))
	for k := range embedders {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ResetForTest clears the registry. Test-only helper.
func ResetForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	providers = map[string]providerFactory{}
	embedders = map[string]embedderFactory{}
}
