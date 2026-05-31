// Package providers is the LLM abstraction layer. Each upstream vendor
// (Anthropic, OpenAI, Gemini, Ollama, ...) ships as a sub-package that
// registers a Provider implementation. The rest of vor depends only
// on this package's interfaces — never directly on a vendor SDK — so
// swapping or adding providers is local work.
package providers

import (
	"context"
	"errors"
	"time"
)

// Role classifies a message in a multi-turn conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn of a chat-style request.
type Message struct {
	Role    Role
	Content string

	// CacheControl is an Anthropic-specific hint: when true the provider
	// should emit `cache_control: ephemeral` so the prefix up to this
	// message becomes cacheable. Other providers ignore it.
	CacheControl bool
}

// Request describes a single generation call.
type Request struct {
	Model     string
	System    string
	Messages  []Message
	MaxTokens int

	// Temperature in [0.0, 2.0]. Zero means "use provider default".
	Temperature float64

	// StopSequences halt generation when produced.
	StopSequences []string

	// Operation is a free-form label that flows into llm_costs.operation
	// for accounting (e.g. "page", "answer", "decision_mining").
	Operation string

	// Metadata carries provider-specific knobs the caller may surface; the
	// generic interface doesn't interpret them.
	Metadata map[string]string

	// FilePath is an optional repo-relative file association — used by the
	// cost-tracking layer to attribute spend to a specific file.
	FilePath string
}

// Usage is the token accounting returned alongside a generation.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CachedTokens int
}

// Response is the result of a generation call.
type Response struct {
	Model      string
	Content    string
	StopReason string
	Usage      Usage
	Latency    time.Duration
}

// StreamEvent is emitted by GenerateStream. Exactly one of TextDelta,
// Usage, or Err is meaningful per event.
type StreamEvent struct {
	TextDelta string
	Usage     *Usage
	Err       error
}

// Provider is the contract every vendor implementation satisfies.
type Provider interface {
	// Name returns the provider identifier (e.g. "anthropic", "openai").
	// Stable; persisted to wiki_pages.provider_name and llm_costs.
	Name() string

	// Models lists the models this provider can serve. May be empty when
	// the provider supports arbitrary model strings (LiteLLM, Ollama).
	Models() []string

	// Generate executes the request synchronously.
	Generate(ctx context.Context, req Request) (Response, error)

	// GenerateStream is like Generate but emits incremental events on the
	// returned channel. The channel is closed when generation completes.
	// Providers that don't natively stream may emulate by sending one
	// TextDelta + Usage.
	GenerateStream(ctx context.Context, req Request) (<-chan StreamEvent, error)
}

// BatchProvider is an optional capability: some providers (Anthropic
// Messages Batch, OpenAI Batch) offer a half-price async path with
// 24-hour SLA. Implementations should embed Provider.
type BatchProvider interface {
	Provider

	// BatchGenerate accepts a list of requests, submits them as a batch,
	// and returns responses in the same order. Implementations may block
	// for the entire batch lifetime or return when results are ready.
	BatchGenerate(ctx context.Context, reqs []Request) ([]Response, error)
}

// Embedder is the dual of Provider for vector embeddings. Lives here
// (rather than its own package) because embedding has the same registry
// and rate-limit needs as generation.
type Embedder interface {
	// Name returns the embedder identifier (e.g. "openai", "gemini",
	// "mock").
	Name() string

	// Model returns the model the embedder was constructed with.
	Model() string

	// Dimensions returns the embedding vector dimensionality.
	Dimensions() int

	// Embed turns each string into a vector. Returns one vector per
	// input, in the same order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// ----- Errors --------------------------------------------------------------

// ErrTransient marks a recoverable error that the retry layer should wrap
// in backoff (network blips, 5xx, 429 rate limits). Providers should
// classify their errors so the retry helper can decide whether to retry.
var ErrTransient = errors.New("transient provider error")

// ErrUnauthenticated indicates missing or invalid API credentials. Never
// retried.
var ErrUnauthenticated = errors.New("unauthenticated")

// ErrModelNotFound indicates the requested model isn't supported by the
// provider. Never retried.
var ErrModelNotFound = errors.New("model not found")

// ErrContextTooLong indicates the request exceeded the model's context
// window. Never retried.
var ErrContextTooLong = errors.New("context too long")

// ErrAccountFatal marks an account-level failure that will recur identically
// for every request until the account is fixed — exhausted credits, disabled
// billing, payment required. Never retried, and signals batch callers (e.g.
// wiki generation) to abort rather than attempt the remaining work.
var ErrAccountFatal = errors.New("account error")

// IsTransient classifies an error for the retry layer. Implementations
// can wrap their errors with ErrTransient; this helper inspects via
// errors.Is.
func IsTransient(err error) bool {
	return errors.Is(err, ErrTransient)
}

// IsFatal reports whether err dooms an entire batch of provider calls: missing
// or invalid credentials, an unknown model, or an account-level billing
// failure. These recur identically per request, so a batch caller should stop
// instead of attempting the rest. Per-request failures — ErrTransient (retried)
// and ErrContextTooLong (depends on the specific input) — are not fatal.
func IsFatal(err error) bool {
	return errors.Is(err, ErrUnauthenticated) ||
		errors.Is(err, ErrModelNotFound) ||
		errors.Is(err, ErrAccountFatal)
}
