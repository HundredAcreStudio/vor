package providerfactory_test

import (
	"testing"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/providerfactory"
)

func TestProviderReady(t *testing.T) {
	// Isolate the environment: only a Gemini key is present.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "g-key")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("VOR_LITELLM_BASE_URL", "")
	cfg := config.Config{}

	cases := []struct {
		name        string
		wantReady   bool
		wantRequire string
	}{
		{"google", true, "GEMINI_API_KEY or GOOGLE_API_KEY"}, // matches the Gemini key
		{"anthropic", false, "ANTHROPIC_API_KEY"},
		{"openai", false, "OPENAI_API_KEY"},
		{"ollama", true, ""},                       // local; no key needed
		{"litellm", false, "VOR_LITELLM_BASE_URL"}, // needs a base URL
	}
	for _, tc := range cases {
		ready, requires := providerfactory.ProviderReady(tc.name, cfg)
		if ready != tc.wantReady {
			t.Errorf("ProviderReady(%q) ready = %v, want %v", tc.name, ready, tc.wantReady)
		}
		if requires != tc.wantRequire {
			t.Errorf("ProviderReady(%q) requires = %q, want %q", tc.name, requires, tc.wantRequire)
		}
	}
}

func TestEmbedderReady(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "g-key")
	t.Setenv("GOOGLE_API_KEY", "")
	cfg := config.Config{}

	if ready, _ := providerfactory.EmbedderReady("google", cfg); !ready {
		t.Error("google embedder should be ready with a Gemini key")
	}
	if ready, req := providerfactory.EmbedderReady("openai", cfg); ready || req != "OPENAI_API_KEY" {
		t.Errorf("openai embedder: ready=%v requires=%q, want false/OPENAI_API_KEY", ready, req)
	}
	if ready, _ := providerfactory.EmbedderReady("ollama", cfg); !ready {
		t.Error("ollama embedder should be ready (local, no key)")
	}
}
