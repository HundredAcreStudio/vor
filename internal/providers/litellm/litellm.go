// Package litellm registers a Provider that talks to a LiteLLM proxy.
// LiteLLM exposes an OpenAI-compatible /v1/chat/completions surface, so
// this is a thin wrapper over the openai package's NewCompatible client —
// the only differences are the provider name (for cost/persistence
// attribution) and that base_url is required (a proxy has no canonical
// public endpoint). api_key is optional: some proxies are keyless, others
// expect the LiteLLM virtual key in the Bearer header.
//
// Construction is via providers.NewProvider("litellm", opts) with:
//
//	"base_url"      string  (required — the proxy URL, also VOR_LITELLM_BASE_URL)
//	"api_key"       string  (optional — LiteLLM virtual key)
//	"default_model" string  (optional — proxy model alias)
//	"http_client"   *http.Client (optional — tests)
package litellm

import (
	"github.com/HundredAcreStudio/vor/internal/providers"
	"github.com/HundredAcreStudio/vor/internal/providers/openai"
)

const providerName = "litellm"

func init() {
	providers.RegisterProvider(providerName, func(opts providers.Options) (providers.Provider, error) {
		return openai.NewCompatible(providerName, opts)
	})
}
