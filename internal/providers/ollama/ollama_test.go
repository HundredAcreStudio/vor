package ollama_test

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
	_ "github.com/HundredAcreStudio/vor/internal/providers/ollama"
)

func newServer(t *testing.T, h http.HandlerFunc) providers.Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	prov, err := providers.NewProvider("ollama", providers.Options{"base_url": srv.URL})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return prov
}

func TestRegistry(t *testing.T) {
	if !contains(providers.RegisteredProviders(), "ollama") {
		t.Errorf("ollama provider not registered")
	}
	if !contains(providers.RegisteredEmbedders(), "ollama") {
		t.Errorf("ollama embedder not registered")
	}
}

func TestNewProvider_NoKeyRequired(t *testing.T) {
	if _, err := providers.NewProvider("ollama", providers.Options{}); err != nil {
		t.Errorf("ollama should construct without a key: %v", err)
	}
}

func TestGenerate_SuccessPath(t *testing.T) {
	prov := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if req["stream"] != false {
			t.Errorf("expected stream=false for Generate")
		}
		msgs := req["messages"].([]any)
		if m0 := msgs[0].(map[string]any); m0["role"] != "system" {
			t.Errorf("system should be first message, got %v", m0)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":             "llama3.2",
			"message":           map[string]any{"role": "assistant", "content": "hello"},
			"done":              true,
			"done_reason":       "stop",
			"prompt_eval_count": 7,
			"eval_count":        2,
		})
	})
	resp, err := prov.Generate(context.Background(), providers.Request{
		System:   "be brief",
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Content != "hello" || resp.StopReason != "stop" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 2 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestGenerateStream_NDJSON(t *testing.T) {
	prov := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"message":{"content":"Hel"},"done":false}`+"\n")
		_, _ = io.WriteString(w, `{"message":{"content":"lo"},"done":false}`+"\n")
		_, _ = io.WriteString(w, `{"message":{"content":""},"done":true,"prompt_eval_count":4,"eval_count":2}`+"\n")
	})
	ch, err := prov.GenerateStream(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	var sb strings.Builder
	var usage *providers.Usage
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
		sb.WriteString(ev.TextDelta)
		if ev.Usage != nil {
			usage = ev.Usage
		}
	}
	if sb.String() != "Hello" {
		t.Errorf("text = %q", sb.String())
	}
	if usage == nil || usage.OutputTokens != 2 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestGenerate_ModelNotFound(t *testing.T) {
	prov := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"model 'foo' not found, try pulling it first"}`)
	})
	_, err := prov.Generate(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "x"}},
	})
	if !errors.Is(err, providers.ErrModelNotFound) {
		t.Errorf("want ErrModelNotFound, got %v", err)
	}
}

func TestEmbedder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float32{{0.1, 0.2}, {0.3, 0.4}},
		})
	}))
	t.Cleanup(srv.Close)
	emb, err := providers.NewEmbedder("ollama", providers.Options{"base_url": srv.URL})
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	vecs, err := emb.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || vecs[0][1] != 0.2 {
		t.Errorf("vectors = %v", vecs)
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
