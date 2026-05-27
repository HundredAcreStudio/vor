package context

import (
	"strings"
)

// SymbolBundle is the input to a PageKindSymbolDetail generation. One
// page per symbol: a function, class, method, or any non-file node from
// the graph. The bundle carries enough of the surrounding source to
// produce a grounded explanation without forcing the model to re-read
// the whole file.
type SymbolBundle struct {
	// RepoRoot is the absolute path to the repository root.
	RepoRoot string

	// ID is the graph node identifier ("file.go::SymbolName" by
	// convention). Used as the page target so each symbol gets a unique
	// row.
	ID string

	// Name is the human-facing symbol name without qualification.
	Name string

	// QualifiedName is the full path (e.g. "package.Class.method").
	QualifiedName string

	// Kind is the symbol kind from the graph (function|class|method|…).
	Kind string

	// FilePath is the repo-relative path that defines the symbol.
	FilePath string

	// Language is the source language of FilePath.
	Language string

	// StartLine / EndLine bracket the symbol's source span. 1-indexed.
	StartLine int
	EndLine   int

	// SourceSpan is the source text between StartLine and EndLine
	// inclusive. Capped via TruncateForPrompt by the template — but
	// most symbol bodies are small, so truncation is rare.
	SourceSpan string

	// Callers names the symbols / files that reference this symbol
	// (inbound edges).
	Callers []string

	// Callees names the symbols / files this symbol references
	// (outbound edges).
	Callees []string

	// SourceHash is computed over SourceSpan so the page can detect
	// when the symbol body has changed even if the surrounding file
	// hasn't.
	SourceHash string
}

// NewSymbolBundle builds a SymbolBundle from caller-supplied source
// text and metadata. The runner reads file contents and slices out the
// span; this constructor just hashes it.
func NewSymbolBundle(repoRoot, id, name, qualified, kind, filePath, language string, startLine, endLine int, sourceSpan string, callers, callees []string) SymbolBundle {
	return SymbolBundle{
		RepoRoot:      repoRoot,
		ID:            id,
		Name:          name,
		QualifiedName: qualified,
		Kind:          kind,
		FilePath:      filePath,
		Language:      language,
		StartLine:     startLine,
		EndLine:       endLine,
		SourceSpan:    sourceSpan,
		Callers:       callers,
		Callees:       callees,
		SourceHash:    HashSource(sourceSpan),
	}
}

// SliceLines extracts the 1-indexed line range [start, end] from the
// supplied file content. Bounds are clamped — a slice past EOF returns
// what's available rather than erroring, since stale graph rows
// pointing at since-shrunk files shouldn't break a generation pass.
func SliceLines(content string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end < start {
		end = start
	}
	lines := strings.Split(content, "\n")
	if start > len(lines) {
		return ""
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n")
}
