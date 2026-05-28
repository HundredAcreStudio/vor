package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/providers"
	_ "github.com/repowise-dev/repowise-go/internal/providers/google"
)

func newServer(t *testing.T, h http.HandlerFunc) providers.Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	prov, err := providers.NewProvider("google", providers.Options{
		"api_key": "test-key", "base_url": srv.URL,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return prov
}

func TestRegistry(t *testing.T) {
	if !contains(providers.RegisteredProviders(), "google") {
		t.Errorf("google provider not registered")
	}
	if !contains(providers.RegisteredEmbedders(), "google") {
		t.Errorf("google embedder not registered")
	}
}

func TestNewProvider_RequiresKey(t *testing.T) {
	_, err := providers.NewProvider("google", providers.Options{})
	if !errors.Is(err, providers.ErrUnauthenticated) {
		t.Errorf("want ErrUnauthenticated, got %v", err)
	}
}

func TestGenerate_SuccessPath(t *testing.T) {
	prov := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("api key header = %q", r.Header.Get("x-goog-api-key"))
		}
		if !strings.HasSuffix(r.URL.Path, ":generateContent") {
			t.Errorf("path = %s", r.URL.Path)
		}
		if !strings.Contains(r.URL.Path, "gemini-1.5-pro") {
			t.Errorf("model not in path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if req["systemInstruction"] == nil {
			t.Errorf("system not encoded as systemInstruction")
		}
		contents := req["contents"].([]any)
		c0 := contents[0].(map[string]any)
		if c0["role"] != "user" {
			t.Errorf("first content role = %v", c0["role"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []any{map[string]any{
				"content":      map[string]any{"role": "model", "parts": []any{map[string]any{"text": "answer"}}},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 8, "candidatesTokenCount": 4},
		})
	})
	resp, err := prov.Generate(context.Background(), providers.Request{
		Model: "gemini-1.5-pro", System: "be brief",
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Content != "answer" || resp.StopReason != "STOP" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage.InputTokens != 8 || resp.Usage.OutputTokens != 4 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestGenerate_RoleMapping(t *testing.T) {
	prov := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Contents []struct {
				Role string `json:"role"`
			} `json:"contents"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.Contents) != 2 || req.Contents[1].Role != "model" {
			t.Errorf("assistant role should map to 'model': %+v", req.Contents)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "ok"}}}}},
		})
	})
	_, err := prov.Generate(context.Background(), providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "q"},
			{Role: providers.RoleAssistant, Content: "a"},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestGenerate_ErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   error
	}{
		{403, `{"error":{"code":403,"message":"API key not valid","status":"PERMISSION_DENIED"}}`, providers.ErrUnauthenticated},
		{429, `{"error":{"message":"quota"}}`, providers.ErrTransient},
		{503, `{"error":{"message":"overloaded"}}`, providers.ErrTransient},
		{404, `{"error":{"message":"model not found","status":"NOT_FOUND"}}`, providers.ErrModelNotFound},
	}
	for _, tc := range cases {
		prov := newServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		})
		_, err := prov.Generate(context.Background(), providers.Request{
			Messages: []providers.Message{{Role: providers.RoleUser, Content: "x"}},
		})
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: got %v want %v", tc.status, err, tc.want)
		}
	}
}

func TestGenerateStream(t *testing.T) {
	prov := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path+"?"+r.URL.RawQuery, ":streamGenerateContent") {
			t.Errorf("expected stream endpoint, got %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"text":"lo"}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}}`+"\n\n")
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

func TestEmbedder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ":batchEmbedContents") {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": []any{
				map[string]any{"values": []float32{0.1, 0.2}},
				map[string]any{"values": []float32{0.3, 0.4}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	emb, err := providers.NewEmbedder("google", providers.Options{
		"api_key": "k", "base_url": srv.URL, "dimensions": 2,
	})
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	vecs, err := emb.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || vecs[1][1] != 0.4 {
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
