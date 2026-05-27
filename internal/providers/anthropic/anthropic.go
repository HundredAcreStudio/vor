// Package anthropic is the production Provider implementation for the
// Anthropic Messages API. It speaks the v1 Messages format directly over
// net/http — no SDK dependency, since Anthropic doesn't ship an official
// Go SDK and third-party ones drift behind the API.
//
// Capabilities:
//
//   - Generate / GenerateStream against the /v1/messages endpoint
//   - Prompt-cache emission via Message.CacheControl (cache_control:
//     ephemeral) plus cache-aware Usage reporting
//   - Error classification: 401/403→ErrUnauthenticated, 404 on model
//     →ErrModelNotFound, 400 context-length→ErrContextTooLong,
//     429/5xx→ErrTransient (retryable)
//   - Configurable base URL + HTTP client for tests
//
// Construction is via providers.NewProvider("anthropic", opts) with:
//
//	"api_key"       string  (required, also AnthropicAPIKey env)
//	"base_url"      string  (optional, defaults to api.anthropic.com)
//	"version"       string  (optional, defaults "2023-06-01")
//	"default_model" string  (optional, defaults claude-sonnet-4-6)
//	"http_client"   *http.Client (optional — tests)
package anthropic

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
	providerName     = "anthropic"
	defaultBaseURL   = "https://api.anthropic.com"
	defaultVersion   = "2023-06-01"
	defaultModel     = "claude-sonnet-4-6"
	messagesEndpoint = "/v1/messages"
)

func init() {
	providers.RegisterProvider(providerName, newProvider)
}

// Provider is the concrete Anthropic implementation. Safe for concurrent
// use across goroutines (http.Client is, and we hold no mutable state).
type Provider struct {
	apiKey       string
	baseURL      string
	apiVersion   string
	defaultModel string
	http         *http.Client
}

func newProvider(opts providers.Options) (providers.Provider, error) {
	key, _ := opts["api_key"].(string)
	if key == "" {
		return nil, fmt.Errorf("%w: anthropic api_key is required", providers.ErrUnauthenticated)
	}
	base := defaultBaseURL
	if b, ok := opts["base_url"].(string); ok && b != "" {
		base = strings.TrimRight(b, "/")
	}
	version := defaultVersion
	if v, ok := opts["version"].(string); ok && v != "" {
		version = v
	}
	model := defaultModel
	if m, ok := opts["default_model"].(string); ok && m != "" {
		model = m
	}
	client, _ := opts["http_client"].(*http.Client)
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Provider{
		apiKey:       key,
		baseURL:      base,
		apiVersion:   version,
		defaultModel: model,
		http:         client,
	}, nil
}

func (p *Provider) Name() string { return providerName }

// Models returns the model identifiers this provider expects. The list is
// a hint for UI surfaces — the API itself accepts arbitrary model strings,
// so users can pass through new versions without a code change.
func (p *Provider) Models() []string {
	return []string{
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		"claude-haiku-4-5",
		"claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022",
		"claude-3-opus-20240229",
	}
}

// ---- Wire types — kept private to this package ---------------------------

type contentBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
}

type wireMessage struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type wireRequest struct {
	Model         string         `json:"model"`
	MaxTokens     int            `json:"max_tokens"`
	Temperature   float64        `json:"temperature,omitempty"`
	System        []contentBlock `json:"system,omitempty"`
	Messages      []wireMessage  `json:"messages"`
	StopSequences []string       `json:"stop_sequences,omitempty"`
	Stream        bool           `json:"stream,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type wireUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type wireResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      wireUsage      `json:"usage"`
}

type wireError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type wireErrorEnvelope struct {
	Type  string    `json:"type"`
	Error wireError `json:"error"`
}

// ---- Generate ------------------------------------------------------------

// Generate executes a synchronous request against POST /v1/messages.
func (p *Provider) Generate(ctx context.Context, req providers.Request) (providers.Response, error) {
	body, err := json.Marshal(p.buildRequest(req, false))
	if err != nil {
		return providers.Response{}, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	httpReq, err := p.newRequest(ctx, body)
	if err != nil {
		return providers.Response{}, err
	}
	start := time.Now()
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return providers.Response{}, fmt.Errorf("anthropic: %w: %v", providers.ErrTransient, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return providers.Response{}, fmt.Errorf("anthropic: read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return providers.Response{}, classifyError(resp.StatusCode, respBody)
	}

	var w wireResponse
	if err := json.Unmarshal(respBody, &w); err != nil {
		return providers.Response{}, fmt.Errorf("anthropic: decode response: %w", err)
	}

	out := providers.Response{
		Model:      w.Model,
		Content:    joinTextBlocks(w.Content),
		StopReason: w.StopReason,
		Usage: providers.Usage{
			InputTokens:  w.Usage.InputTokens,
			OutputTokens: w.Usage.OutputTokens,
			// CachedTokens captures both creation + read so accounting picks
			// up the (cheaper) cached-read tokens for cost estimation. The
			// cost layer multiplies by the cached-read rate.
			CachedTokens: w.Usage.CacheReadInputTokens,
		},
		Latency: time.Since(start),
	}
	return out, nil
}

// ---- GenerateStream ------------------------------------------------------

// GenerateStream issues a streaming request and emits TextDelta / Usage
// events on the returned channel. The channel is closed when the stream
// finishes (cleanly or with an error event).
func (p *Provider) GenerateStream(ctx context.Context, req providers.Request) (<-chan providers.StreamEvent, error) {
	body, err := json.Marshal(p.buildRequest(req, true))
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	httpReq, err := p.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w: %v", providers.ErrTransient, err)
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

// streamSSE parses the SSE stream and emits decoded events into ch.
// Anthropic's event types we care about:
//
//   message_start         — carries initial usage (input_tokens)
//   content_block_delta   — text delta on a content block
//   message_delta         — carries final usage (output_tokens)
//   message_stop          — terminal event
//   error                 — terminal error event
func streamSSE(ctx context.Context, body io.Reader, ch chan<- providers.StreamEvent) {
	reader := bufio.NewReader(body)
	usage := providers.Usage{}
	var eventName string
	var dataBuf strings.Builder

	flush := func() {
		if eventName == "" {
			return
		}
		data := dataBuf.String()
		dataBuf.Reset()
		switch eventName {
		case "content_block_delta":
			text := extractDeltaText(data)
			if text != "" {
				select {
				case <-ctx.Done():
				case ch <- providers.StreamEvent{TextDelta: text}:
				}
			}
		case "message_start":
			if u, ok := extractUsage(data, "message"); ok {
				usage.InputTokens = u.InputTokens
				usage.CachedTokens = u.CacheReadInputTokens
			}
		case "message_delta":
			if u, ok := extractUsage(data, "usage"); ok {
				if u.OutputTokens > 0 {
					usage.OutputTokens = u.OutputTokens
				}
			}
		case "message_stop":
			finalUsage := usage
			ch <- providers.StreamEvent{Usage: &finalUsage}
		case "error":
			ch <- providers.StreamEvent{Err: fmt.Errorf("anthropic: stream error: %s", data)}
		}
		eventName = ""
	}

	for {
		if err := ctx.Err(); err != nil {
			ch <- providers.StreamEvent{Err: err}
			return
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			flush()
			if !errors.Is(err, io.EOF) {
				ch <- providers.StreamEvent{Err: err}
			}
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event: "):
			eventName = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}
}

// extractDeltaText pulls the text out of a content_block_delta payload.
// Payload shape: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"..."}}
func extractDeltaText(payload string) string {
	var p struct {
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return ""
	}
	if p.Delta.Type != "text_delta" {
		return ""
	}
	return p.Delta.Text
}

// extractUsage pulls the usage block from a message_start or message_delta
// payload. The key differs between the two: message_start nests it under
// "message", message_delta under "usage".
func extractUsage(payload, container string) (wireUsage, bool) {
	switch container {
	case "message":
		var p struct {
			Message struct {
				Usage wireUsage `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(payload), &p); err == nil {
			return p.Message.Usage, true
		}
	case "usage":
		var p struct {
			Usage wireUsage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &p); err == nil {
			return p.Usage, true
		}
	}
	return wireUsage{}, false
}

// ---- Request helpers ----------------------------------------------------

func (p *Provider) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	url := p.baseURL + messagesEndpoint
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-api-key", p.apiKey)
	r.Header.Set("anthropic-version", p.apiVersion)
	return r, nil
}

func (p *Provider) buildRequest(req providers.Request, stream bool) wireRequest {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		// Anthropic requires max_tokens; pick a sane default just under the
		// minimum window so callers that forget still get something useful.
		maxTokens = 4096
	}
	wr := wireRequest{
		Model:         model,
		MaxTokens:     maxTokens,
		Temperature:   req.Temperature,
		Messages:      messagesToWire(req.Messages),
		StopSequences: req.StopSequences,
		Stream:        stream,
	}
	if req.System != "" {
		wr.System = []contentBlock{{Type: "text", Text: req.System}}
	}
	if len(req.Metadata) > 0 {
		md := make(map[string]any, len(req.Metadata))
		for k, v := range req.Metadata {
			md[k] = v
		}
		wr.Metadata = md
	}
	return wr
}

func messagesToWire(msgs []providers.Message) []wireMessage {
	out := make([]wireMessage, 0, len(msgs))
	for _, m := range msgs {
		role := string(m.Role)
		// System messages are encoded via wireRequest.System, not as a
		// turn. Filter them out here.
		if role == string(providers.RoleSystem) {
			continue
		}
		block := contentBlock{Type: "text", Text: m.Content}
		if m.CacheControl {
			block.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		out = append(out, wireMessage{Role: role, Content: []contentBlock{block}})
	}
	return out
}

func joinTextBlocks(blocks []contentBlock) string {
	if len(blocks) == 1 {
		return blocks[0].Text
	}
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// ---- Error classification -----------------------------------------------

// classifyError maps an Anthropic API error to the canonical sentinel
// errors so the retry layer can decide what to do. Body is best-effort
// parsed for a structured error envelope.
func classifyError(status int, body []byte) error {
	var env wireErrorEnvelope
	_ = json.Unmarshal(body, &env)
	msg := env.Error.Message
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}

	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return fmt.Errorf("anthropic %d: %w: %s", status, providers.ErrUnauthenticated, msg)
	case status == http.StatusNotFound:
		if strings.Contains(strings.ToLower(env.Error.Type), "model") ||
			strings.Contains(strings.ToLower(msg), "model") {
			return fmt.Errorf("anthropic %d: %w: %s", status, providers.ErrModelNotFound, msg)
		}
	case status == http.StatusBadRequest:
		lower := strings.ToLower(msg)
		if strings.Contains(lower, "context") && (strings.Contains(lower, "long") ||
			strings.Contains(lower, "exceed") || strings.Contains(lower, "limit")) {
			return fmt.Errorf("anthropic %d: %w: %s", status, providers.ErrContextTooLong, msg)
		}
	case status == http.StatusTooManyRequests, status >= 500:
		return fmt.Errorf("anthropic %d: %w: %s", status, providers.ErrTransient, msg)
	}
	return fmt.Errorf("anthropic %d: %s", status, msg)
}
