package litellm_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/providers"
	_ "github.com/HundredAcreStudio/vor/internal/providers/litellm"
)

func TestRegistered(t *testing.T) {
	found := false
	for _, n := range providers.RegisteredProviders() {
		if n == "litellm" {
			found = true
		}
	}
	if !found {
		t.Fatalf("litellm not registered: %v", providers.RegisteredProviders())
	}
}

func TestRequiresBaseURL(t *testing.T) {
	_, err := providers.NewProvider("litellm", providers.Options{"api_key": "k"})
	if err == nil {
		t.Fatal("expected error without base_url")
	}
}

func TestDelegatesToOpenAIWire(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		// Optional virtual key passes through as a Bearer token.
		if r.Header.Get("Authorization") != "Bearer vkey" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		_, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "proxy-alias",
			"choices": []any{map[string]any{"message": map[string]any{"content": "ok"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	t.Cleanup(srv.Close)

	prov, err := providers.NewProvider("litellm", providers.Options{
		"base_url": srv.URL, "api_key": "vkey",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if prov.Name() != "litellm" {
		t.Errorf("name = %q, want litellm (for cost attribution)", prov.Name())
	}
	resp, err := prov.Generate(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("content = %q", resp.Content)
	}
}
