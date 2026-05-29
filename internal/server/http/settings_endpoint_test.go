package http_test

import (
	"net/http"
	"strings"
	"testing"

	// Register the ollama provider so providerfactory can construct it in the
	// gate test (the real binary registers all providers via cli/commands).
	_ "github.com/HundredAcreStudio/vor/internal/providers/ollama"
)

// TestGlobalSettings_SetProviderFlipsGate exercises the global settings
// endpoint: setting a global provider that needs no key (ollama) makes the
// global provider gate flip true and shows up as the effective provider.
func TestGlobalSettings_SetProviderFlipsGate(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	srv, _ := fixtureRepo(t)

	// Default global provider is anthropic with no key → gate false.
	var before struct {
		Global struct {
			ProviderConfigured bool `json:"providerConfigured"`
		} `json:"global"`
		ProviderKeys map[string]bool `json:"providerKeys"`
	}
	hitJSON(t, srv.URL, "/api/settings", &before)
	if before.Global.ProviderConfigured {
		t.Fatal("provider gate should be false before configuring one")
	}
	if before.ProviderKeys == nil {
		t.Error("response should include providerKeys detection")
	}

	// Set the global provider to ollama (needs no API key).
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings/provider", strings.NewReader(`"ollama"`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", resp.StatusCode)
	}

	// Now the global provider gate flips true and effective reflects ollama.
	var after struct {
		Effective struct {
			Provider string `json:"provider"`
		} `json:"effective"`
		Global struct {
			ProviderConfigured bool `json:"providerConfigured"`
		} `json:"global"`
	}
	hitJSON(t, srv.URL, "/api/settings", &after)
	if after.Effective.Provider != "ollama" {
		t.Errorf("effective provider = %q, want ollama", after.Effective.Provider)
	}
	if !after.Global.ProviderConfigured {
		t.Error("provider gate should be true after setting a keyless provider")
	}
}

// TestSettingsEndpoint_GlobalGate verifies the per-repo settings response
// carries the global provider/embedder gate and the selectable option lists
// the dashboard uses to render (and gate) per-repo provider/embedder controls.
func TestSettingsEndpoint_GlobalGate(t *testing.T) {
	// Clear provider keys so the global gate is deterministic: the default
	// global provider is "anthropic" (needs a key) and embedder is "mock"
	// (treated as unconfigured), so both gates resolve false here.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	srv, repoID := fixtureRepo(t)

	var body struct {
		Global struct {
			ProviderConfigured bool `json:"providerConfigured"`
			EmbedderConfigured bool `json:"embedderConfigured"`
		} `json:"global"`
		ProviderOptions []string `json:"providerOptions"`
		EmbedderOptions []string `json:"embedderOptions"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/settings", &body)

	if body.Global.ProviderConfigured {
		t.Error("providerConfigured should be false with no API key globally")
	}
	if body.Global.EmbedderConfigured {
		t.Error("embedderConfigured should be false (default embedder is mock)")
	}
	if !contains(body.ProviderOptions, "anthropic") {
		t.Errorf("providerOptions missing anthropic: %v", body.ProviderOptions)
	}
	if !contains(body.EmbedderOptions, "openai") {
		t.Errorf("embedderOptions missing openai: %v", body.EmbedderOptions)
	}
	// "mock" must never be offered as a selectable per-repo choice.
	if contains(body.ProviderOptions, "mock") || contains(body.EmbedderOptions, "mock") {
		t.Errorf("options should not include mock: providers=%v embedders=%v",
			body.ProviderOptions, body.EmbedderOptions)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
