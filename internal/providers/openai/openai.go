// Package openai is the production Provider + Embedder for the OpenAI
// Chat Completions and Embeddings APIs, spoken directly over net/http —
// no SDK dependency, matching the anthropic package's approach.
//
// The same wire format is used by a large ecosystem of OpenAI-compatible
// servers (LiteLLM proxy, vLLM, LocalAI, Together, Groq, …), so the
// client is parameterised by name + base URL and exported via
// NewCompatible for the litellm package to reuse.
//
// Construction is via providers.NewProvider("openai", opts) with:
//
//	"api_key"       string  (required for openai, also OPENAI_API_KEY env)
//	"base_url"      string  (optional, defaults to api.openai.com)
//	"default_model" string  (optional, defaults gpt-4o)
//	"organization"  string  (optional, OpenAI-Organization header)
//	"http_client"   *http.Client (optional — tests)
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/HundredAcreStudio/vor/internal/providers"
)

const (
	providerName       = "openai"
	defaultBaseURL     = "https://api.openai.com"
	defaultModel       = "gpt-4o"
	chatEndpoint       = "/v1/chat/completions"
	embeddingsEndpoint = "/v1/embeddings"
	defaultEmbedModel  = "text-embedding-3-small"
	defaultEmbedDim    = 1536
	streamDoneSentinel = "[DONE]"
)

func init() {
	providers.RegisterProvider(providerName, newProvider)
	providers.RegisterEmbedder(providerName, newEmbedder)
	providers.RegisterProviderModels(providerName, providers.ModelInfo{
		Default: "gpt-4o-mini", // cost-effective default for generation
		Models: []string{
			"gpt-4o", "gpt-4o-mini", "gpt-4.1", "gpt-4.1-mini",
			"gpt-4-turbo", "o3", "o3-mini", "o1",
		},
	})
	providers.RegisterEmbedderModels(providerName, providers.ModelInfo{
		Default: "text-embedding-3-small",
		Models:  []string{"text-embedding-3-small", "text-embedding-3-large", "text-embedding-ada-002"},
	})
}

// Provider is the concrete OpenAI(-compatible) implementation. Safe for
// concurrent use (http.Client is, and we hold no mutable state).
type Provider struct {
	name         string
	apiKey       string
	baseURL      string
	organization string
	defaultModel string
	http         *http.Client
}

func newProvider(opts providers.Options) (providers.Provider, error) {
	return build(providerName, defaultBaseURL, true, opts)
}

// NewCompatible builds an OpenAI-wire-format provider under a custom name
// and base URL. Used by the litellm package (and available to any other
// OpenAI-compatible target). The api_key is optional here — proxies often
// authenticate differently or not at all — but base_url is required since
// there is no canonical default.
func NewCompatible(name string, opts providers.Options) (providers.Provider, error) {
	if b, _ := opts["base_url"].(string); strings.TrimSpace(b) == "" {
		return nil, fmt.Errorf("%s: base_url is required", name)
	}
	return build(name, "", false, opts)
}

func build(name, baseDefault string, requireKey bool, opts providers.Options) (providers.Provider, error) {
	key, _ := opts["api_key"].(string)
	if requireKey && key == "" {
		return nil, fmt.Errorf("%w: %s api_key is required", providers.ErrUnauthenticated, name)
	}
	base := baseDefault
	if b, ok := opts["base_url"].(string); ok && b != "" {
		base = strings.TrimRight(b, "/")
	}
	model := defaultModel
	if m, ok := opts["default_model"].(string); ok && m != "" {
		model = m
	}
	org, _ := opts["organization"].(string)
	client, _ := opts["http_client"].(*http.Client)
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Provider{
		name:         name,
		apiKey:       key,
		baseURL:      base,
		organization: org,
		defaultModel: model,
		http:         client,
	}, nil
}

func (p *Provider) Name() string { return p.name }

// Models is a UI hint; the API accepts arbitrary model strings so new
// releases work without a code change.
func (p *Provider) Models() []string {
	return []string{
		"gpt-4o", "gpt-4o-mini", "gpt-4.1", "gpt-4.1-mini",
		"gpt-4-turbo", "o3", "o3-mini", "o1",
	}
}

// ---- Wire types ----------------------------------------------------------

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireRequest struct {
	Model         string         `json:"model"`
	Messages      []wireMessage  `json:"messages"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Temperature   float64        `json:"temperature,omitempty"`
	Stop          []string       `json:"stop,omitempty"`
	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type wireUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type wireChoice struct {
	Index        int         `json:"index"`
	Message      wireMessage `json:"message"`
	Delta        wireMessage `json:"delta"`
	FinishReason string      `json:"finish_reason"`
}

type wireResponse struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Choices []wireChoice `json:"choices"`
	Usage   *wireUsage   `json:"usage"`
}

type wireError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

type wireErrorEnvelope struct {
	Error wireError `json:"error"`
}

// ---- Generate ------------------------------------------------------------

// Generate executes a synchronous request against POST /v1/chat/completions.
func (p *Provider) Generate(ctx context.Context, req providers.Request) (providers.Response, error) {
	body, err := json.Marshal(p.buildRequest(req, false))
	if err != nil {
		return providers.Response{}, fmt.Errorf("%s: marshal request: %w", p.name, err)
	}
	httpReq, err := p.newRequest(ctx, chatEndpoint, body)
	if err != nil {
		return providers.Response{}, err
	}
	start := time.Now()
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return providers.Response{}, fmt.Errorf("%s: %w: %v", p.name, providers.ErrTransient, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return providers.Response{}, fmt.Errorf("%s: read body: %w", p.name, err)
	}
	if resp.StatusCode >= 400 {
		return providers.Response{}, p.classifyError(resp.StatusCode, respBody)
	}

	var w wireResponse
	if err := json.Unmarshal(respBody, &w); err != nil {
		return providers.Response{}, fmt.Errorf("%s: decode response: %w", p.name, err)
	}
	content, finish := "", ""
	if len(w.Choices) > 0 {
		content = w.Choices[0].Message.Content
		finish = w.Choices[0].FinishReason
	}
	out := providers.Response{
		Model:      w.Model,
		Content:    content,
		StopReason: finish,
		Latency:    time.Since(start),
	}
	if w.Usage != nil {
		out.Usage = providers.Usage{
			InputTokens:  w.Usage.PromptTokens,
			OutputTokens: w.Usage.CompletionTokens,
			CachedTokens: w.Usage.PromptTokensDetails.CachedTokens,
		}
	}
	return out, nil
}

// ---- GenerateStream ------------------------------------------------------

// GenerateStream issues a streaming chat request and emits TextDelta /
// Usage events. stream_options.include_usage asks OpenAI to send a final
// usage-only chunk; compatible proxies honour it, others simply omit it.
func (p *Provider) GenerateStream(ctx context.Context, req providers.Request) (<-chan providers.StreamEvent, error) {
	body, err := json.Marshal(p.buildRequest(req, true))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", p.name, err)
	}
	httpReq, err := p.newRequest(ctx, chatEndpoint, body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %v", p.name, providers.ErrTransient, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, p.classifyError(resp.StatusCode, errBody)
	}

	ch := make(chan providers.StreamEvent, 32)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		streamSSE(ctx, resp.Body, ch)
	}()
	return ch, nil
}

// streamSSE parses OpenAI's `data: {chunk}\n\n` SSE stream. Each chunk is a
// wireResponse-shaped object carrying choices[].delta.content and, on the
// final usage chunk, a populated usage block. The stream ends with a
// `data: [DONE]` line.
func streamSSE(ctx context.Context, body io.Reader, ch chan<- providers.StreamEvent) {
	reader := bufio.NewReader(body)
	var usage *providers.Usage
	for {
		if err := ctx.Err(); err != nil {
			ch <- providers.StreamEvent{Err: err}
			return
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			if usage != nil {
				ch <- providers.StreamEvent{Usage: usage}
			}
			if !errors.Is(err, io.EOF) {
				ch <- providers.StreamEvent{Err: err}
			}
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == streamDoneSentinel {
			if usage != nil {
				ch <- providers.StreamEvent{Usage: usage}
				usage = nil
			}
			continue
		}
		var chunk wireResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			if delta := chunk.Choices[0].Delta.Content; delta != "" {
				select {
				case <-ctx.Done():
					ch <- providers.StreamEvent{Err: ctx.Err()}
					return
				case ch <- providers.StreamEvent{TextDelta: delta}:
				}
			}
		}
		if chunk.Usage != nil {
			usage = &providers.Usage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
				CachedTokens: chunk.Usage.PromptTokensDetails.CachedTokens,
			}
		}
	}
}

// ---- Request helpers -----------------------------------------------------

func (p *Provider) newRequest(ctx context.Context, endpoint string, body []byte) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", p.name, err)
	}
	r.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		r.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	if p.organization != "" {
		r.Header.Set("OpenAI-Organization", p.organization)
	}
	return r, nil
}

func (p *Provider) buildRequest(req providers.Request, stream bool) wireRequest {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	wr := wireRequest{
		Model:       model,
		Messages:    messagesToWire(req.System, req.Messages),
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stop:        req.StopSequences,
		Stream:      stream,
	}
	if stream {
		wr.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	return wr
}

// messagesToWire prepends the system prompt as a system-role message (the
// OpenAI convention) and passes the rest through unchanged.
func messagesToWire(system string, msgs []providers.Message) []wireMessage {
	out := make([]wireMessage, 0, len(msgs)+1)
	if system != "" {
		out = append(out, wireMessage{Role: string(providers.RoleSystem), Content: system})
	}
	for _, m := range msgs {
		out = append(out, wireMessage{Role: string(m.Role), Content: m.Content})
	}
	return out
}

// ---- Embedder ------------------------------------------------------------

// Embedder implements providers.Embedder against POST /v1/embeddings.
type Embedder struct {
	apiKey  string
	baseURL string
	model   string
	dim     int
	http    *http.Client
}

func newEmbedder(opts providers.Options) (providers.Embedder, error) {
	key, _ := opts["api_key"].(string)
	if key == "" {
		return nil, fmt.Errorf("%w: openai embedder api_key is required", providers.ErrUnauthenticated)
	}
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
	return &Embedder{apiKey: key, baseURL: base, model: model, dim: dim, http: client}, nil
}

func (e *Embedder) Name() string    { return providerName }
func (e *Embedder) Model() string   { return e.model }
func (e *Embedder) Dimensions() int { return e.dim }

type embedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Model string `json:"model"`
}

// Embed turns each text into a vector. The dimensions field is sent only
// when it differs from the model's native size; text-embedding-3-* honour
// it (Matryoshka truncation), older models reject it.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	reqBody := embedRequest{Model: e.model, Input: texts}
	if strings.HasPrefix(e.model, "text-embedding-3") {
		reqBody.Dimensions = e.dim
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("openai embed: marshal: %w", err)
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+embeddingsEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai embed: build request: %w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.http.Do(r)
	if err != nil {
		return nil, fmt.Errorf("openai embed: %w: %v", providers.ErrTransient, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai embed: read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, classifyEmbedError(resp.StatusCode, respBody)
	}
	var w embedResponse
	if err := json.Unmarshal(respBody, &w); err != nil {
		return nil, fmt.Errorf("openai embed: decode: %w", err)
	}
	// The API may return data out of order; sort by index to align with input.
	sort.Slice(w.Data, func(i, j int) bool { return w.Data[i].Index < w.Data[j].Index })
	out := make([][]float32, len(w.Data))
	for i, d := range w.Data {
		out[i] = d.Embedding
	}
	if len(out) != len(texts) {
		return nil, fmt.Errorf("openai embed: got %d vectors for %d inputs", len(out), len(texts))
	}
	return out, nil
}

// ---- Error classification ------------------------------------------------

func (p *Provider) classifyError(status int, body []byte) error {
	return classify(p.name, status, body)
}

func classifyEmbedError(status int, body []byte) error {
	return classify("openai embed", status, body)
}

// classify maps an OpenAI API error to the canonical sentinels so the retry
// layer can decide whether to back off.
func classify(prefix string, status int, body []byte) error {
	var env wireErrorEnvelope
	_ = json.Unmarshal(body, &env)
	msg := env.Error.Message
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	lower := strings.ToLower(msg + " " + env.Error.Code + " " + env.Error.Type)
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return fmt.Errorf("%s %d: %w: %s", prefix, status, providers.ErrUnauthenticated, msg)
	case status == http.StatusNotFound:
		if strings.Contains(lower, "model") {
			return fmt.Errorf("%s %d: %w: %s", prefix, status, providers.ErrModelNotFound, msg)
		}
	case status == http.StatusBadRequest:
		if strings.Contains(lower, "context") || strings.Contains(lower, "maximum context") ||
			strings.Contains(lower, "too many tokens") || strings.Contains(lower, "context_length_exceeded") {
			return fmt.Errorf("%s %d: %w: %s", prefix, status, providers.ErrContextTooLong, msg)
		}
		if strings.Contains(lower, "model") && strings.Contains(lower, "does not exist") {
			return fmt.Errorf("%s %d: %w: %s", prefix, status, providers.ErrModelNotFound, msg)
		}
	case status == http.StatusTooManyRequests, status >= 500:
		return fmt.Errorf("%s %d: %w: %s", prefix, status, providers.ErrTransient, msg)
	}
	return fmt.Errorf("%s %d: %s", prefix, status, msg)
}
