// Package google is the production Provider + Embedder for Google's
// Gemini API (Generative Language API, v1beta), spoken directly over
// net/http — no SDK dependency, matching the other provider packages.
//
// Gemini's wire format differs from OpenAI/Anthropic: turns are
// "contents" with "parts", the assistant role is "model", the system
// prompt is a top-level "systemInstruction", and usage is reported as
// "usageMetadata". The API key is sent via the x-goog-api-key header.
//
// Construction is via providers.NewProvider("google", opts) with:
//
//	"api_key"       string  (required, also GEMINI_API_KEY / GOOGLE_API_KEY env)
//	"base_url"      string  (optional, defaults to generativelanguage.googleapis.com)
//	"default_model" string  (optional, defaults gemini-2.0-flash)
//	"http_client"   *http.Client (optional — tests)
package google

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

	"github.com/repowise-dev/repowise-go/internal/providers"
)

const (
	providerName      = "google"
	defaultBaseURL    = "https://generativelanguage.googleapis.com"
	defaultModel      = "gemini-2.0-flash"
	defaultEmbedModel = "text-embedding-004"
	defaultEmbedDim   = 768
)

func init() {
	providers.RegisterProvider(providerName, newProvider)
	providers.RegisterEmbedder(providerName, newEmbedder)
}

// Provider is the concrete Gemini implementation. Safe for concurrent use.
type Provider struct {
	apiKey       string
	baseURL      string
	defaultModel string
	http         *http.Client
}

func newProvider(opts providers.Options) (providers.Provider, error) {
	key, _ := opts["api_key"].(string)
	if key == "" {
		return nil, fmt.Errorf("%w: google api_key is required", providers.ErrUnauthenticated)
	}
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
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Provider{apiKey: key, baseURL: base, defaultModel: model, http: client}, nil
}

func (p *Provider) Name() string { return providerName }

func (p *Provider) Models() []string {
	return []string{
		"gemini-2.0-flash", "gemini-2.0-flash-lite",
		"gemini-1.5-pro", "gemini-1.5-flash",
	}
}

// ---- Wire types ----------------------------------------------------------

type part struct {
	Text string `json:"text"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type generationConfig struct {
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	Temperature     float64  `json:"temperature,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type wireRequest struct {
	Contents          []content         `json:"contents"`
	SystemInstruction *content          `json:"systemInstruction,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

type usageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
}

type candidate struct {
	Content      content `json:"content"`
	FinishReason string  `json:"finishReason"`
}

type wireResponse struct {
	Candidates    []candidate    `json:"candidates"`
	UsageMetadata *usageMetadata `json:"usageMetadata"`
	ModelVersion  string         `json:"modelVersion"`
}

type wireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

type wireErrorEnvelope struct {
	Error wireError `json:"error"`
}

// ---- Generate ------------------------------------------------------------

// Generate executes POST /v1beta/models/{model}:generateContent.
func (p *Provider) Generate(ctx context.Context, req providers.Request) (providers.Response, error) {
	model := p.modelFor(req)
	body, err := json.Marshal(p.buildRequest(req))
	if err != nil {
		return providers.Response{}, fmt.Errorf("google: marshal request: %w", err)
	}
	endpoint := fmt.Sprintf("/v1beta/models/%s:generateContent", model)
	httpReq, err := p.newRequest(ctx, endpoint, body)
	if err != nil {
		return providers.Response{}, err
	}
	start := time.Now()
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return providers.Response{}, fmt.Errorf("google: %w: %v", providers.ErrTransient, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return providers.Response{}, fmt.Errorf("google: read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return providers.Response{}, classifyError(resp.StatusCode, respBody)
	}
	var w wireResponse
	if err := json.Unmarshal(respBody, &w); err != nil {
		return providers.Response{}, fmt.Errorf("google: decode response: %w", err)
	}
	out := providers.Response{
		Model:      model,
		Content:    joinCandidate(w.Candidates),
		StopReason: candidateFinish(w.Candidates),
		Latency:    time.Since(start),
	}
	if w.UsageMetadata != nil {
		out.Usage = usageFrom(w.UsageMetadata)
	}
	return out, nil
}

// ---- GenerateStream ------------------------------------------------------

// GenerateStream issues :streamGenerateContent?alt=sse and emits text
// deltas plus a final usage event. Gemini reports usageMetadata
// cumulatively per chunk; we keep the last one seen.
func (p *Provider) GenerateStream(ctx context.Context, req providers.Request) (<-chan providers.StreamEvent, error) {
	model := p.modelFor(req)
	body, err := json.Marshal(p.buildRequest(req))
	if err != nil {
		return nil, fmt.Errorf("google: marshal request: %w", err)
	}
	endpoint := fmt.Sprintf("/v1beta/models/%s:streamGenerateContent?alt=sse", model)
	httpReq, err := p.newRequest(ctx, endpoint, body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("google: %w: %v", providers.ErrTransient, err)
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
		streamSSE(ctx, resp.Body, ch)
	}()
	return ch, nil
}

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
		var chunk wireResponse
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
			continue
		}
		if delta := joinCandidate(chunk.Candidates); delta != "" {
			select {
			case <-ctx.Done():
				ch <- providers.StreamEvent{Err: ctx.Err()}
				return
			case ch <- providers.StreamEvent{TextDelta: delta}:
			}
		}
		if chunk.UsageMetadata != nil {
			u := usageFrom(chunk.UsageMetadata)
			usage = &u
		}
	}
}

// ---- Request helpers -----------------------------------------------------

func (p *Provider) modelFor(req providers.Request) string {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	return strings.TrimPrefix(model, "models/")
}

func (p *Provider) newRequest(ctx context.Context, endpoint string, body []byte) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("google: build request: %w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-goog-api-key", p.apiKey)
	return r, nil
}

func (p *Provider) buildRequest(req providers.Request) wireRequest {
	wr := wireRequest{Contents: messagesToContents(req.Messages)}
	if req.System != "" {
		wr.SystemInstruction = &content{Parts: []part{{Text: req.System}}}
	}
	if req.MaxTokens > 0 || req.Temperature > 0 || len(req.StopSequences) > 0 {
		wr.GenerationConfig = &generationConfig{
			MaxOutputTokens: req.MaxTokens,
			Temperature:     req.Temperature,
			StopSequences:   req.StopSequences,
		}
	}
	return wr
}

// messagesToContents maps repowise roles to Gemini's: assistant→model,
// system turns are dropped (encoded as systemInstruction instead).
func messagesToContents(msgs []providers.Message) []content {
	out := make([]content, 0, len(msgs))
	for _, m := range msgs {
		role := "user"
		switch m.Role {
		case providers.RoleAssistant:
			role = "model"
		case providers.RoleSystem:
			continue
		}
		out = append(out, content{Role: role, Parts: []part{{Text: m.Content}}})
	}
	return out
}

func joinCandidate(cands []candidate) string {
	if len(cands) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, pt := range cands[0].Content.Parts {
		sb.WriteString(pt.Text)
	}
	return sb.String()
}

func candidateFinish(cands []candidate) string {
	if len(cands) == 0 {
		return ""
	}
	return cands[0].FinishReason
}

func usageFrom(u *usageMetadata) providers.Usage {
	return providers.Usage{
		InputTokens:  u.PromptTokenCount,
		OutputTokens: u.CandidatesTokenCount,
		CachedTokens: u.CachedContentTokenCount,
	}
}

// ---- Embedder ------------------------------------------------------------

// Embedder implements providers.Embedder against :batchEmbedContents.
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
		return nil, fmt.Errorf("%w: google embedder api_key is required", providers.ErrUnauthenticated)
	}
	base := defaultBaseURL
	if b, ok := opts["base_url"].(string); ok && b != "" {
		base = strings.TrimRight(b, "/")
	}
	model := defaultEmbedModel
	if m, ok := opts["model"].(string); ok && m != "" {
		model = strings.TrimPrefix(m, "models/")
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

type batchEmbedRequestItem struct {
	Model                string  `json:"model"`
	Content              content `json:"content"`
	OutputDimensionality int     `json:"outputDimensionality,omitempty"`
}

type batchEmbedRequest struct {
	Requests []batchEmbedRequestItem `json:"requests"`
}

type batchEmbedResponse struct {
	Embeddings []struct {
		Values []float32 `json:"values"`
	} `json:"embeddings"`
}

// Embed batches all texts into one :batchEmbedContents call, preserving
// input order.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	modelPath := "models/" + e.model
	items := make([]batchEmbedRequestItem, len(texts))
	for i, t := range texts {
		items[i] = batchEmbedRequestItem{
			Model:                modelPath,
			Content:              content{Parts: []part{{Text: t}}},
			OutputDimensionality: e.dim,
		}
	}
	body, err := json.Marshal(batchEmbedRequest{Requests: items})
	if err != nil {
		return nil, fmt.Errorf("google embed: marshal: %w", err)
	}
	endpoint := fmt.Sprintf("/v1beta/models/%s:batchEmbedContents", e.model)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("google embed: build request: %w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-goog-api-key", e.apiKey)
	resp, err := e.http.Do(r)
	if err != nil {
		return nil, fmt.Errorf("google embed: %w: %v", providers.ErrTransient, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("google embed: read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, classifyError(resp.StatusCode, respBody)
	}
	var w batchEmbedResponse
	if err := json.Unmarshal(respBody, &w); err != nil {
		return nil, fmt.Errorf("google embed: decode: %w", err)
	}
	if len(w.Embeddings) != len(texts) {
		return nil, fmt.Errorf("google embed: got %d vectors for %d inputs", len(w.Embeddings), len(texts))
	}
	out := make([][]float32, len(w.Embeddings))
	for i, e := range w.Embeddings {
		out[i] = e.Values
	}
	return out, nil
}

// ---- Error classification ------------------------------------------------

func classifyError(status int, body []byte) error {
	var env wireErrorEnvelope
	_ = json.Unmarshal(body, &env)
	msg := env.Error.Message
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	lower := strings.ToLower(msg + " " + env.Error.Status)
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return fmt.Errorf("google %d: %w: %s", status, providers.ErrUnauthenticated, msg)
	case status == http.StatusNotFound:
		if strings.Contains(lower, "model") {
			return fmt.Errorf("google %d: %w: %s", status, providers.ErrModelNotFound, msg)
		}
	case status == http.StatusBadRequest:
		if strings.Contains(lower, "api key") || strings.Contains(lower, "api_key") {
			return fmt.Errorf("google %d: %w: %s", status, providers.ErrUnauthenticated, msg)
		}
		if strings.Contains(lower, "token") && (strings.Contains(lower, "exceed") || strings.Contains(lower, "maximum")) {
			return fmt.Errorf("google %d: %w: %s", status, providers.ErrContextTooLong, msg)
		}
	case status == http.StatusTooManyRequests, status >= 500:
		return fmt.Errorf("google %d: %w: %s", status, providers.ErrTransient, msg)
	}
	return fmt.Errorf("google %d: %s", status, msg)
}
