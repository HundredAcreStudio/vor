package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/providers"
	_ "github.com/HundredAcreStudio/vor/internal/providers/openai"
)

func newServer(t *testing.T, h http.HandlerFunc) (providers.Provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	prov, err := providers.NewProvider("openai", providers.Options{
		"api_key": "test-key", "base_url": srv.URL,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return prov, srv
}

func TestRegistry_RegistersProviderAndEmbedder(t *testing.T) {
	if !contains(providers.RegisteredProviders(), "openai") {
		t.Errorf("openai provider not registered: %v", providers.RegisteredProviders())
	}
	if !contains(providers.RegisteredEmbedders(), "openai") {
		t.Errorf("openai embedder not registered: %v", providers.RegisteredEmbedders())
	}
}

func TestNewProvider_RequiresAPIKey(t *testing.T) {
	_, err := providers.NewProvider("openai", providers.Options{})
	if !errors.Is(err, providers.ErrUnauthenticated) {
		t.Errorf("expected ErrUnauthenticated, got %v", err)
	}
}

func TestGenerate_SuccessPath(t *testing.T) {
	prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if req["model"] != "gpt-4o" {
			t.Errorf("model = %v", req["model"])
		}
		msgs := req["messages"].([]any)
		if len(msgs) != 2 {
			t.Fatalf("want system+user = 2 messages, got %d", len(msgs))
		}
		if m0 := msgs[0].(map[string]any); m0["role"] != "system" || m0["content"] != "be terse" {
			t.Errorf("first message should be the system prompt, got %v", m0)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "gpt-4o-2024-08-06",
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "hi there"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3},
		})
	})
	resp, err := prov.Generate(context.Background(), providers.Request{
		Model: "gpt-4o", System: "be terse",
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Content != "hi there" {
		t.Errorf("content = %q", resp.Content)
	}
	if resp.StopReason != "stop" {
		t.Errorf("stop reason = %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 3 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestGenerate_ErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   error
	}{
		{401, `{"error":{"message":"bad key","type":"auth"}}`, providers.ErrUnauthenticated},
		{429, `{"error":{"message":"slow down"}}`, providers.ErrTransient},
		{500, `{"error":{"message":"boom"}}`, providers.ErrTransient},
		{400, `{"error":{"message":"maximum context length exceeded","code":"context_length_exceeded"}}`, providers.ErrContextTooLong},
		{404, `{"error":{"message":"model gpt-9 does not exist"}}`, providers.ErrModelNotFound},
	}
	for _, tc := range cases {
		prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		})
		_, err := prov.Generate(context.Background(), providers.Request{
			Messages: []providers.Message{{Role: providers.RoleUser, Content: "x"}},
		})
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: got %v, want %v", tc.status, err, tc.want)
		}
	}
}

func TestGenerateStream_EmitsDeltasAndUsage(t *testing.T) {
	prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"choices":[{"index":0,"delta":{"content":"Hel"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"lo"}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		}
		for _, c := range chunks {
			_, _ = io.WriteString(w, "data: "+c+"\n\n")
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	ch, err := prov.GenerateStream(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	var text strings.Builder
	var usage *providers.Usage
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
		if ev.TextDelta != "" {
			text.WriteString(ev.TextDelta)
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
	}
	if text.String() != "Hello" {
		t.Errorf("streamed text = %q", text.String())
	}
	if usage == nil || usage.InputTokens != 5 || usage.OutputTokens != 2 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestEmbedder_Embed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if req["model"] != "text-embedding-3-small" {
			t.Errorf("model = %v", req["model"])
		}
		// dimensions should be sent for text-embedding-3-*.
		if req["dimensions"] == nil {
			t.Errorf("expected dimensions for text-embedding-3-*")
		}
		// Return out of order to exercise index sorting.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "text-embedding-3-small",
			"data": []any{
				map[string]any{"index": 1, "embedding": []float32{0.3, 0.4}},
				map[string]any{"index": 0, "embedding": []float32{0.1, 0.2}},
			},
		})
	}))
	t.Cleanup(srv.Close)

	emb, err := providers.NewEmbedder("openai", providers.Options{
		"api_key": "k", "base_url": srv.URL, "dimensions": 2,
	})
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	vecs, err := emb.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || vecs[0][0] != 0.1 || vecs[1][0] != 0.3 {
		t.Errorf("vectors not aligned to input order: %v", vecs)
	}
	if emb.Dimensions() != 2 || emb.Name() != "openai" {
		t.Errorf("embedder metadata: dim=%d name=%s", emb.Dimensions(), emb.Name())
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
