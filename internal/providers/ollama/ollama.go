// Package ollama is the Provider + Embedder for a local Ollama server
// (https://ollama.com), spoken over its native /api/chat and /api/embed
// endpoints. No API key — Ollama runs locally — and generation is free
// (the cost layer returns zero for this provider).
//
// Unlike OpenAI/Gemini, Ollama streams newline-delimited JSON (NDJSON),
// not SSE: each line is a complete chat-response object, the last carrying
// done=true plus token counts.
//
// Construction is via providers.NewProvider("ollama", opts) with:
//
//	"base_url"      string  (optional, defaults to http://localhost:11434, also VOR_OLLAMA_BASE_URL)
//	"default_model" string  (optional, defaults llama3.2)
//	"http_client"   *http.Client (optional — tests)
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/HundredAcreStudio/vor/internal/providers"
)

const (
	providerName      = "ollama"
	defaultBaseURL    = "http://localhost:11434"
	defaultModel      = "llama3.2"
	defaultEmbedModel = "nomic-embed-text"
	defaultEmbedDim   = 768
	chatEndpoint      = "/api/chat"
	embedEndpoint     = "/api/embed"
)

func init() {
	providers.RegisterProvider(providerName, newProvider)
	providers.RegisterEmbedder(providerName, newEmbedder)
	providers.RegisterProviderModels(providerName, providers.ModelInfo{
		Default: "llama3.2", // common local default
		Models:  []string{"llama3.2", "llama3.1", "qwen2.5-coder", "mistral", "gemma2"},
	})
	providers.RegisterEmbedderModels(providerName, providers.ModelInfo{
		Default: "nomic-embed-text",
		Models:  []string{"nomic-embed-text", "mxbai-embed-large", "all-minilm"},
	})
}

// Provider is the concrete Ollama implementation. Safe for concurrent use.
type Provider struct {
	baseURL      string
	defaultModel string
	http         *http.Client
}

func newProvider(opts providers.Options) (providers.Provider, error) {
	base := defaultBaseURL
	if b, ok := opts["base_url"].(string); ok && b != "" {
		base = strings.TrimRight(b, "/")
	}
	model := defaultModel
	if m, ok := opts["default_model"].(string); ok && m != "" {
		model = m
	}
	client, _ := opts["http_client"].(*http.Client)
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	return &Provider{baseURL: base, defaultModel: model, http: client}, nil
}

func (p *Provider) Name() string { return providerName }

// Models returns common defaults; the local server may host any subset.
func (p *Provider) Models() []string {
	return []string{"llama3.2", "llama3.1", "qwen2.5-coder", "mistral", "gemma2"}
}

// ---- Wire types ----------------------------------------------------------

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type wireOptions struct {
	Temperature float64  `json:"temperature,omitempty"`
	NumPredict  int      `json:"num_predict,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

type wireRequest struct {
	Model    string        `json:"model"`
	Messages []wireMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Options  *wireOptions  `json:"options,omitempty"`
}

// chatResponse is one NDJSON line (or the whole body when stream=false).
type chatResponse struct {
	Model           string      `json:"model"`
	Message         wireMessage `json:"message"`
	Done            bool        `json:"done"`
	DoneReason      string      `json:"done_reason"`
	PromptEvalCount int         `json:"prompt_eval_count"`
	EvalCount       int         `json:"eval_count"`
	Error           string      `json:"error"`
}

// ---- Generate ------------------------------------------------------------

// Generate executes POST /api/chat with stream=false.
func (p *Provider) Generate(ctx context.Context, req providers.Request) (providers.Response, error) {
	body, err := json.Marshal(p.buildRequest(req, false))
	if err != nil {
		return providers.Response{}, fmt.Errorf("ollama: marshal request: %w", err)
	}
	httpReq, err := p.newRequest(ctx, chatEndpoint, body)
	if err != nil {
		return providers.Response{}, err
	}
	start := time.Now()
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return providers.Response{}, fmt.Errorf("ollama: %w: %v", providers.ErrTransient, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return providers.Response{}, fmt.Errorf("ollama: read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return providers.Response{}, classifyError(resp.StatusCode, respBody)
	}
	var w chatResponse
	if err := json.Unmarshal(respBody, &w); err != nil {
		return providers.Response{}, fmt.Errorf("ollama: decode response: %w", err)
	}
	return providers.Response{
		Model:      w.Model,
		Content:    w.Message.Content,
		StopReason: w.DoneReason,
		Usage: providers.Usage{
			InputTokens:  w.PromptEvalCount,
			OutputTokens: w.EvalCount,
		},
		Latency: time.Since(start),
	}, nil
}

// ---- GenerateStream ------------------------------------------------------

// GenerateStream issues a stream=true request and parses the NDJSON body,
// emitting one TextDelta per line and a final Usage from the done line.
func (p *Provider) GenerateStream(ctx context.Context, req providers.Request) (<-chan providers.StreamEvent, error) {
	body, err := json.Marshal(p.buildRequest(req, true))
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}
	httpReq, err := p.newRequest(ctx, chatEndpoint, body)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: %w: %v", providers.ErrTransient, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, classifyError(resp.StatusCode, errBody)
	}
	ch := make(chan providers.StreamEvent, 32)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		streamNDJSON(ctx, resp.Body, ch)
	}()
	return ch, nil
}

func streamNDJSON(ctx context.Context, body io.Reader, ch chan<- providers.StreamEvent) {
	reader := bufio.NewReader(body)
	for {
		if err := ctx.Err(); err != nil {
			ch <- providers.StreamEvent{Err: err}
			return
		}
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var chunk chatResponse
			if jErr := json.Unmarshal(bytes.TrimSpace(line), &chunk); jErr == nil {
				if chunk.Error != "" {
					ch <- providers.StreamEvent{Err: fmt.Errorf("ollama: stream error: %s", chunk.Error)}
					return
				}
				if chunk.Message.Content != "" {
					select {
					case <-ctx.Done():
						ch <- providers.StreamEvent{Err: ctx.Err()}
						return
					case ch <- providers.StreamEvent{TextDelta: chunk.Message.Content}:
					}
				}
				if chunk.Done {
					ch <- providers.StreamEvent{Usage: &providers.Usage{
						InputTokens:  chunk.PromptEvalCount,
						OutputTokens: chunk.EvalCount,
					}}
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				ch <- providers.StreamEvent{Err: err}
			}
			return
		}
	}
}

// ---- Request helpers -----------------------------------------------------

func (p *Provider) newRequest(ctx context.Context, endpoint string, body []byte) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	return r, nil
}

func (p *Provider) buildRequest(req providers.Request, stream bool) wireRequest {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	msgs := make([]wireMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, wireMessage{Role: string(providers.RoleSystem), Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, wireMessage{Role: string(m.Role), Content: m.Content})
	}
	wr := wireRequest{Model: model, Messages: msgs, Stream: stream}
	if req.MaxTokens > 0 || req.Temperature > 0 || len(req.StopSequences) > 0 {
		wr.Options = &wireOptions{
			Temperature: req.Temperature,
			NumPredict:  req.MaxTokens,
			Stop:        req.StopSequences,
		}
	}
	return wr
}

// ---- Embedder ------------------------------------------------------------

// Embedder implements providers.Embedder against POST /api/embed.
type Embedder struct {
	baseURL string
	model   string
	dim     int
	http    *http.Client
}

func newEmbedder(opts providers.Options) (providers.Embedder, error) {
	base := defaultBaseURL
	if b, ok := opts["base_url"].(string); ok && b != "" {
		base = strings.TrimRight(b, "/")
	}
	model := defaultEmbedModel
	if m, ok := opts["model"].(string); ok && m != "" {
		model = m
	}
	dim := defaultEmbedDim
	if v, ok := opts["dimensions"].(int); ok && v > 0 {
		dim = v
	}
	client, _ := opts["http_client"].(*http.Client)
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Embedder{baseURL: base, model: model, dim: dim, http: client}, nil
}

func (e *Embedder) Name() string    { return providerName }
func (e *Embedder) Model() string   { return e.model }
func (e *Embedder) Dimensions() int { return e.dim }

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error"`
}

// Embed sends all texts in one /api/embed call. Ollama returns one vector
// per input in order.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(embedRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("ollama embed: marshal: %w", err)
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+embedEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama embed: build request: %w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := e.http.Do(r)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w: %v", providers.ErrTransient, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, classifyError(resp.StatusCode, respBody)
	}
	var w embedResponse
	if err := json.Unmarshal(respBody, &w); err != nil {
		return nil, fmt.Errorf("ollama embed: decode: %w", err)
	}
	if len(w.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama embed: got %d vectors for %d inputs", len(w.Embeddings), len(texts))
	}
	return w.Embeddings, nil
}

// ---- Error classification ------------------------------------------------

func classifyError(status int, body []byte) error {
	var env struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	msg := env.Error
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	lower := strings.ToLower(msg)
	switch {
	case status == http.StatusNotFound:
		// Ollama 404s when a model isn't pulled locally.
		return fmt.Errorf("ollama %d: %w: %s", status, providers.ErrModelNotFound, msg)
	case status == http.StatusBadRequest:
		if strings.Contains(lower, "context") || strings.Contains(lower, "too long") {
			return fmt.Errorf("ollama %d: %w: %s", status, providers.ErrContextTooLong, msg)
		}
	case status == http.StatusTooManyRequests, status >= 500:
		return fmt.Errorf("ollama %d: %w: %s", status, providers.ErrTransient, msg)
	}
	return fmt.Errorf("ollama %d: %s", status, msg)
}
