// Package parser is the AST extraction layer. It defines a per-language
// Parser interface and a registry-backed ASTParser that dispatches to the
// right implementation based on the file's language tag.
//
// Per-language parsers live in subdirectories under this package (parser/golang,
// parser/python, ...). Each subpackage registers itself with init() and
// imports tree-sitter grammar bindings via cgo.
package parser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/repowise-dev/repowise-go/internal/ingestion/languages"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// Parser is the per-language extractor. Implementations should be safe for
// concurrent use by multiple goroutines if the caller intends to parallelise
// parsing (the default ASTParser does not).
type Parser interface {
	// Parse extracts symbols, imports, calls, heritage, and exports from
	// source. The fi.Language is guaranteed to match the parser's
	// registered language.
	Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[models.LanguageTag]Parser{}
)

// Register installs p as the parser for the given language. Intended to be
// called from sub-package init() functions. Panics on a double-registration
// because that is a programming error.
func Register(lang models.LanguageTag, p Parser) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[lang]; exists {
		panic(fmt.Sprintf("parser already registered for %q", lang))
	}
	registry[lang] = p
}

// LookupParser returns the parser for the given language, or nil if none is
// registered.
func LookupParser(lang models.LanguageTag) Parser {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[lang]
}

// RegisteredLanguages returns the tags that currently have a parser.
// Order is unspecified.
func RegisteredLanguages() []models.LanguageTag {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]models.LanguageTag, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// ErrNoParser is returned when ASTParser is asked to parse a language for
// which no parser is registered (and which isn't a passthrough/special
// handler).
var ErrNoParser = errors.New("no parser registered for language")

// ASTParser is the unified entry point. It picks the right Parser from the
// registry, runs it, and stamps the ContentHash on the resulting ParsedFile.
type ASTParser struct{}

// New returns a new ASTParser. There is no per-instance state today; the
// type exists so the API has room to grow (e.g. progress callbacks).
func New() *ASTParser { return &ASTParser{} }

// Parse dispatches to the language-appropriate parser.
//
// Passthrough languages (markdown, yaml, json, ...) return a minimal
// ParsedFile carrying only the FileInfo and content hash — the rest of the
// pipeline still indexes them for blame and search but does not look for
// AST extractions.
func (a *ASTParser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	if err := ctx.Err(); err != nil {
		return models.ParsedFile{}, err
	}

	spec := languages.Lookup(fi.Language)
	if spec == nil {
		return models.ParsedFile{}, fmt.Errorf("unknown language: %q", fi.Language)
	}

	// Passthrough: no AST work to do.
	if spec.IsPassthrough {
		return models.ParsedFile{
			FileInfo:    fi,
			ContentHash: hash(source),
		}, nil
	}

	p := LookupParser(fi.Language)
	if p == nil {
		// Special handlers (Dockerfile, Makefile, OpenAPI) will eventually
		// register Parser implementations from their own packages. Until
		// they do, treat them as passthrough for now.
		if spec.Tag == "dockerfile" || spec.Tag == "makefile" || spec.Tag == "openapi" {
			return models.ParsedFile{
				FileInfo:    fi,
				ContentHash: hash(source),
				ParseErrors: []string{fmt.Sprintf("no parser for %q yet (returning empty parse)", fi.Language)},
			}, nil
		}
		return models.ParsedFile{}, fmt.Errorf("%w: %s", ErrNoParser, fi.Language)
	}

	parsed, err := p.Parse(ctx, fi, source)
	if err != nil {
		return parsed, err
	}
	parsed.FileInfo = fi
	if parsed.ContentHash == "" {
		parsed.ContentHash = hash(source)
	}
	return parsed, nil
}

// hash returns the SHA-256 hex digest of source. Matches the Python
// implementation, which stores this on ParsedFile.content_hash.
func hash(source []byte) string {
	sum := sha256.Sum256(source)
	return hex.EncodeToString(sum[:])
}
