// Package resolver is the modular per-language import resolver layer.
//
// Each language with non-trivial import semantics (Go module paths, Rust
// crate paths, Java package paths, TS tsconfig aliases, ...) registers a
// Resolver in init() via Register. The graph builder consults the
// registry once per import; absent a registered resolver, the import
// produces no graph edges.
//
// This mirrors the parser registry (internal/ingestion/parser) and the
// manifest extractor registry (internal/ingestion/external) — same
// pattern so adding a new language stays a localised change.
package resolver

import (
	"sync"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// Resolver maps one Import into target file paths in the analyzed set.
// Returning zero paths means "this import didn't resolve to anything in
// the repo" — typically an external dependency (stdlib, third-party).
type Resolver interface {
	// Language returns the language this resolver handles. One resolver
	// per language; calling Register twice with the same Language() panics.
	Language() models.LanguageTag

	// Resolve returns target file paths for imp. ctx carries the file
	// index plus any language-specific configuration the resolver needs.
	Resolve(fi models.FileInfo, imp models.Import, ctx Context) []string
}

// Context is the read-only view of the analyzed repository that all
// resolvers see. Language-specific fields are namespaced by language
// prefix (GoModulePath, RustCrateName, ...) — each resolver consumes only
// what it needs.
//
// Adding a new language-specific field here is part of adding that
// language's resolver. Resolvers that don't need a field ignore it.
type Context struct {
	// Files is the set of analyzed file paths (repo-relative POSIX).
	Files map[string]bool

	// FilesByDir indexes Files by their containing directory. Empty
	// directory keys (root files) use ".". Used by package-scoped
	// resolvers (Go, Java) to look up every file in a package.
	FilesByDir map[string][]string

	// GoModulePath is the `module ...` declaration from go.mod when
	// present. Empty string disables Go module-path resolution.
	GoModulePath string

	// RustCrateName is the [package].name from Cargo.toml when present.
	// Used by the Rust resolver to match `<crate_name>::...` imports in
	// addition to the literal `crate::...` prefix.
	RustCrateName string

	// JavaSourceRoots are directory prefixes treated as source roots
	// when resolving Java's dotted imports
	// ("com.example.foo.Bar" → src/main/java/com/example/foo/Bar.java).
	// Defaults to ["src/main/java", "src", ""] when the caller leaves
	// this empty.
	JavaSourceRoots []string

	// CppIncludeDirs are directories to try when resolving
	// `#include "header.h"` after the importer's own directory.
	// Defaults to ["include", ""] when empty.
	CppIncludeDirs []string
}

var (
	registryMu sync.RWMutex
	registry   = map[models.LanguageTag]Resolver{}
)

// Register installs r under r.Language(). Panics on duplicate
// registration — a programming error.
func Register(r Resolver) {
	registryMu.Lock()
	defer registryMu.Unlock()
	lang := r.Language()
	if _, exists := registry[lang]; exists {
		panic("resolver already registered for " + string(lang))
	}
	registry[lang] = r
}

// Lookup returns the resolver for lang, or nil.
func Lookup(lang models.LanguageTag) Resolver {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[lang]
}

// Registered returns the set of languages with registered resolvers.
// Stable iteration: the slice is rebuilt per call.
func Registered() []models.LanguageTag {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]models.LanguageTag, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// ResetForTest clears the registry. Test-only.
func ResetForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[models.LanguageTag]Resolver{}
}
