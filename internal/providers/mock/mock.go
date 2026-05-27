// Package mock is the deterministic provider + embedder used by tests and
// by the default repowise config. It produces hash-stable output for any
// given input so test fixtures remain reproducible.
package mock

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/repowise-dev/repowise-go/internal/providers"
)

const (
	providerName = "mock"
	embedderName = "mock"
	defaultModel = "mock-1"
	defaultDim   = 32
)

func init() {
	providers.RegisterProvider(providerName, newProvider)
	providers.RegisterEmbedder(embedderName, newEmbedder)
}

// ---- Provider --------------------------------------------------------------

// Provider implements providers.Provider with deterministic, no-side-effect
// generation. Useful for tests, smoke runs, and benchmarks that need a
// real provider object without a real API call.
type Provider struct {
	model string
}

func newProvider(opts providers.Options) (providers.Provider, error) {
	model := defaultModel
	if m, ok := opts["model"].(string); ok && m != "" {
		model = m
	}
	return &Provider{model: model}, nil
}

func (p *Provider) Name() string     { return providerName }
func (p *Provider) Models() []string { return []string{defaultModel, "mock-large"} }

// Generate echoes a deterministic transform of the last user message.
// Output format: "[mock:<model>] <hash-prefix>: <user content first 80 chars>".
// Useful for asserting end-to-end plumbing without coupling to real
// model behaviour.
func (p *Provider) Generate(ctx context.Context, req providers.Request) (providers.Response, error) {
	if err := ctx.Err(); err != nil {
		return providers.Response{}, err
	}
	start := time.Now()
	user := lastUserContent(req.Messages)
	prefix := hashPrefix(user, 8)
	trimmed := user
	if len(trimmed) > 80 {
		trimmed = trimmed[:80]
	}
	content := fmt.Sprintf("[%s:%s] %s: %s", providerName, p.model, prefix, trimmed)

	inputTokens := tokenEstimate(req.System) + tokenEstimate(user)
	outputTokens := tokenEstimate(content)

	return providers.Response{
		Model:      p.model,
		Content:    content,
		StopReason: "end_turn",
		Usage: providers.Usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
		},
		Latency: time.Since(start),
	}, nil
}

// GenerateStream chunks Generate's output into 4-char text-deltas, plus a
// terminating Usage event. Lets the streaming code path be exercised in
// tests without a real network round-trip.
func (p *Provider) GenerateStream(ctx context.Context, req providers.Request) (<-chan providers.StreamEvent, error) {
	resp, err := p.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan providers.StreamEvent, 8)
	go func() {
		defer close(ch)
		for i := 0; i < len(resp.Content); i += 4 {
			end := i + 4
			if end > len(resp.Content) {
				end = len(resp.Content)
			}
			select {
			case <-ctx.Done():
				ch <- providers.StreamEvent{Err: ctx.Err()}
				return
			case ch <- providers.StreamEvent{TextDelta: resp.Content[i:end]}:
			}
		}
		usage := resp.Usage
		ch <- providers.StreamEvent{Usage: &usage}
	}()
	return ch, nil
}

// ---- Embedder --------------------------------------------------------------

// Embedder implements providers.Embedder with deterministic vectors.
// Vectors are derived from a SHA-256 of the input, then unpacked into
// fixed-dimensional floats — same input always produces same vector.
type Embedder struct {
	model string
	dim   int
}

func newEmbedder(opts providers.Options) (providers.Embedder, error) {
	dim := defaultDim
	if v, ok := opts["dimensions"].(int); ok && v > 0 {
		dim = v
	}
	model := "mock-embed-1"
	if m, ok := opts["model"].(string); ok && m != "" {
		model = m
	}
	return &Embedder{model: model, dim: dim}, nil
}

func (e *Embedder) Name() string    { return embedderName }
func (e *Embedder) Model() string   { return e.model }
func (e *Embedder) Dimensions() int { return e.dim }

func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = deterministicVector(t, e.dim)
	}
	return out, nil
}

// ---- helpers ---------------------------------------------------------------

func lastUserContent(msgs []providers.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == providers.RoleUser {
			return msgs[i].Content
		}
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1].Content
	}
	return ""
}

func hashPrefix(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	hexstr := hex.EncodeToString(sum[:])
	if n > len(hexstr) {
		n = len(hexstr)
	}
	return hexstr[:n]
}

// tokenEstimate is the simple "4 chars ≈ 1 token" rule. Good enough for
// the mock to produce non-zero usage counts.
func tokenEstimate(s string) int {
	t := (len(s) + 3) / 4
	if t < 1 {
		t = 1
	}
	return t
}

// deterministicVector hashes the input and expands the 32 hash bytes into
// `dim` floats in [-1.0, 1.0). For dim > 32 the hash is repeatedly fed
// back through SHA-256 to extend.
func deterministicVector(s string, dim int) []float32 {
	out := make([]float32, dim)
	seed := sha256.Sum256([]byte(s))
	src := seed[:]
	pos := 0
	for pos < dim {
		// Convert 4-byte chunks to float32 in [-1, 1).
		for i := 0; i+4 <= len(src) && pos < dim; i += 4 {
			u := binary.LittleEndian.Uint32(src[i : i+4])
			f := (float32(u) / float32(^uint32(0))) * 2.0 - 1.0
			out[pos] = f
			pos++
		}
		if pos < dim {
			next := sha256.Sum256(src)
			src = next[:]
		}
	}
	return out
}

// Ensure the package's helpers are reachable from tests without importing
// strings here.
var _ = strings.TrimSpace
