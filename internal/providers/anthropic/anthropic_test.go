package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HundredAcreStudio/vor/internal/providers"
	_ "github.com/HundredAcreStudio/vor/internal/providers/anthropic"
)

// newServer spins up a mock Anthropic endpoint with a custom handler.
// The returned Provider points at it.
func newServer(t *testing.T, handler http.HandlerFunc) (providers.Provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	prov, err := providers.NewProvider("anthropic", providers.Options{
		"api_key":  "test-key",
		"base_url": srv.URL,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return prov, srv
}

func TestRegistry_RegistersOnImport(t *testing.T) {
	if !contains(providers.RegisteredProviders(), "anthropic") {
		t.Fatalf("anthropic should be registered after side-effect import: %v",
			providers.RegisteredProviders())
	}
}

func TestNewProvider_RequiresAPIKey(t *testing.T) {
	_, err := providers.NewProvider("anthropic", providers.Options{})
	if err == nil {
		t.Fatal("expected error without api_key")
	}
	if !errors.Is(err, providers.ErrUnauthenticated) {
		t.Errorf("expected ErrUnauthenticated, got %v", err)
	}
}

func TestGenerate_SuccessPath(t *testing.T) {
	prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify auth headers.
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("x-api-key header = %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing anthropic-version header")
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}

		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if req["model"] != "claude-opus-4-7" {
			t.Errorf("model = %v", req["model"])
		}
		if req["system"] == nil {
			t.Errorf("system not encoded in request")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_1","type":"message","role":"assistant",
			"model":"claude-opus-4-7",
			"content":[{"type":"text","text":"Hello back"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":15,"output_tokens":3,"cache_read_input_tokens":7}
		}`))
	})
	resp, err := prov.Generate(context.Background(), providers.Request{
		Model:    "claude-opus-4-7",
		System:   "You are a helper.",
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Content != "Hello back" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.Usage.InputTokens != 15 || resp.Usage.OutputTokens != 3 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if resp.Usage.CachedTokens != 7 {
		t.Errorf("CachedTokens = %d, want 7", resp.Usage.CachedTokens)
	}
	if resp.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q", resp.Model)
	}
}

func TestGenerate_EmitsCacheControl(t *testing.T) {
	var captured map[string]any
	prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	})
	_, err := prov.Generate(context.Background(), providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "big context", CacheControl: true},
			{Role: providers.RoleAssistant, Content: "ack"},
			{Role: providers.RoleUser, Content: "follow-up"},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Drill into messages[0].content[0].cache_control.type == "ephemeral".
	msgs, _ := captured["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages on wire, got %d", len(msgs))
	}
	first := msgs[0].(map[string]any)
	content := first["content"].([]any)
	block := content[0].(map[string]any)
	cc, ok := block["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("cache_control missing on first message: %+v", block)
	}
	if cc["type"] != "ephemeral" {
		t.Errorf("cache_control type = %v, want ephemeral", cc["type"])
	}
	// Sanity: second message must NOT have cache_control.
	second := msgs[1].(map[string]any)
	if _, has := second["content"].([]any)[0].(map[string]any)["cache_control"]; has {
		t.Errorf("cache_control should be absent on message 1 (Assistant ack)")
	}
}

func TestGenerate_SystemMessageFilteredOut(t *testing.T) {
	var captured map[string]any
	prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	})
	_, err := prov.Generate(context.Background(), providers.Request{
		System: "You are a helper",
		Messages: []providers.Message{
			{Role: providers.RoleSystem, Content: "rogue system msg"},
			{Role: providers.RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := captured["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("RoleSystem messages should be stripped, got %d wire messages", len(msgs))
	}
}

func TestGenerate_Unauthenticated(t *testing.T) {
	prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	})
	_, err := prov.Generate(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if !errors.Is(err, providers.ErrUnauthenticated) {
		t.Errorf("expected ErrUnauthenticated, got %v", err)
	}
}

func TestGenerate_ContextTooLong(t *testing.T) {
	prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: context length exceeded"}}`))
	})
	_, err := prov.Generate(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if !errors.Is(err, providers.ErrContextTooLong) {
		t.Errorf("expected ErrContextTooLong, got %v", err)
	}
}

func TestGenerate_TransientOn5xx(t *testing.T) {
	prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"please retry"}}`))
	})
	_, err := prov.Generate(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if !errors.Is(err, providers.ErrTransient) {
		t.Errorf("expected ErrTransient, got %v", err)
	}
}

func TestGenerate_TransientOnRateLimit(t *testing.T) {
	prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
	})
	_, err := prov.Generate(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if !errors.Is(err, providers.ErrTransient) {
		t.Errorf("expected ErrTransient, got %v", err)
	}
}

func TestGenerate_ModelNotFound(t *testing.T) {
	prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"model claude-xyz-unknown not found"}}`))
	})
	_, err := prov.Generate(context.Background(), providers.Request{
		Model:    "claude-xyz-unknown",
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if !errors.Is(err, providers.ErrModelNotFound) {
		t.Errorf("expected ErrModelNotFound, got %v", err)
	}
}

func TestGenerateStream_StreamsDeltasAndUsage(t *testing.T) {
	prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		emit := func(event, data string) {
			_, _ = w.Write([]byte("event: " + event + "\n"))
			_, _ = w.Write([]byte("data: " + data + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
		emit("message_start", `{"type":"message_start","message":{"id":"m","model":"claude-sonnet-4-6","usage":{"input_tokens":10,"output_tokens":0,"cache_read_input_tokens":2}}}`)
		emit("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`)
		emit("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`)
		emit("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)
		emit("message_stop", `{"type":"message_stop"}`)
	})
	ch, err := prov.GenerateStream(context.Background(), providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	var sb strings.Builder
	var finalUsage providers.Usage
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
		if ev.TextDelta != "" {
			sb.WriteString(ev.TextDelta)
		}
		if ev.Usage != nil {
			finalUsage = *ev.Usage
		}
	}
	if sb.String() != "Hello world" {
		t.Errorf("streamed text = %q", sb.String())
	}
	if finalUsage.InputTokens != 10 || finalUsage.OutputTokens != 5 || finalUsage.CachedTokens != 2 {
		t.Errorf("final usage = %+v", finalUsage)
	}
}

func TestGenerateStream_ContextCancelStops(t *testing.T) {
	var calls int32
	prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		// Stream slow forever, until the client gives up.
		for {
			if r.Context().Err() != nil {
				return
			}
			_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ch, err := prov.GenerateStream(ctx, providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return // channel closed — stream ended as expected
			}
			if ev.Err != nil {
				return // error (typically context.Canceled) — also acceptable
			}
		case <-deadline:
			t.Fatalf("stream did not stop after context cancel (calls=%d)", atomic.LoadInt32(&calls))
		}
	}
}

func TestModels_NonEmpty(t *testing.T) {
	prov, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {})
	if len(prov.Models()) == 0 {
		t.Error("Models() should be non-empty for the anthropic provider")
	}
	if prov.Name() != "anthropic" {
		t.Errorf("Name() = %q", prov.Name())
	}
}

// contains is a tiny slice-contains helper to avoid pulling slices into
// every test.
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
