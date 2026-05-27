// Package common holds helpers shared by per-language parsers. It exists to
// keep each language's parser file focused on language-specific quirks (kind
// mapping, visibility rules, receiver extraction) instead of plumbing.
package common

import (
	"fmt"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/queries"
)

// LoadQueryOnce lazily compiles the named .scm query against lang and caches
// the result inside the supplied sync.Once / pointer pair. Per-language
// parser packages hold the cache state as package-level vars.
//
// Usage:
//
//	var (
//	    pyQuery     *sitter.Query
//	    pyQueryOnce sync.Once
//	    pyQueryErr  error
//	)
//	q, err := common.LoadQueryOnce("python.scm", python.GetLanguage(), &pyQueryOnce, &pyQuery, &pyQueryErr)
func LoadQueryOnce(name string, lang *sitter.Language, once *sync.Once, ptr **sitter.Query, errPtr *error) (*sitter.Query, error) {
	once.Do(func() {
		data, err := queries.Get(name)
		if err != nil {
			*errPtr = err
			return
		}
		q, err := sitter.NewQuery(data, lang)
		if err != nil {
			*errPtr = fmt.Errorf("compile %s: %w", name, err)
			return
		}
		*ptr = q
	})
	return *ptr, *errPtr
}

// BucketCaptures groups a match's captures by capture name. The returned map
// allocates only what it needs and is safe to mutate.
func BucketCaptures(m *sitter.QueryMatch, query *sitter.Query) map[string][]*sitter.Node {
	out := map[string][]*sitter.Node{}
	for _, c := range m.Captures {
		name := query.CaptureNameForId(c.Index)
		out[name] = append(out[name], c.Node)
	}
	return out
}

// EnclosingSymbolID returns a pointer to the ID of the smallest symbol whose
// line range contains line, or nil if none does. Used to populate
// CallSite.CallerSymbolID.
func EnclosingSymbolID(syms []models.Symbol, line int) *string {
	var best *models.Symbol
	for i := range syms {
		s := &syms[i]
		if line < s.StartLine || line > s.EndLine {
			continue
		}
		if best == nil || (s.EndLine-s.StartLine) < (best.EndLine-best.StartLine) {
			best = s
		}
	}
	if best == nil {
		return nil
	}
	id := best.ID
	return &id
}

// SortByStartLine sorts symbols in-place by ascending start line. Tiny
// insertion sort: most files have fewer than 100 symbols.
func SortByStartLine(syms []models.Symbol) {
	for i := 1; i < len(syms); i++ {
		for j := i; j > 0 && syms[j].StartLine < syms[j-1].StartLine; j-- {
			syms[j], syms[j-1] = syms[j-1], syms[j]
		}
	}
}

// PtrStr returns a heap pointer to s. Convenience for *string fields.
func PtrStr(s string) *string { return &s }

// PtrInt returns a heap pointer to n. Convenience for *int fields.
func PtrInt(n int) *int { return &n }

// FirstLine returns slice up to the first newline (or the whole slice if no
// newline is present). Used to derive a single-line signature.
func FirstLine(s []byte) []byte {
	for i := range s {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// SignatureSlice returns the source text from def's start through the first
// of (params end, def end), then truncated at the first newline. Mirrors the
// Python implementation's signature heuristic.
func SignatureSlice(source []byte, def *sitter.Node, params []*sitter.Node) string {
	start := def.StartByte()
	end := def.EndByte()
	if len(params) > 0 && params[0].EndByte() > start {
		end = params[0].EndByte()
	}
	if int(end) > len(source) {
		end = uint32(len(source))
	}
	return string(FirstLine(source[start:end]))
}
