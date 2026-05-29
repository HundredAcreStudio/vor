package common

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

// ParseSpec supplies the language-specific pieces of the shared extraction
// shell. RunQuery owns the universal flow — tree-sitter parse, query exec, the
// capture-dispatch loop, symbol collection, line sort, and enclosing-symbol
// resolution for call sites — and calls back into these for the parts that
// differ per language.
//
// This is the lower-level driver shared by every parser. Partial-tier
// languages reach it indirectly through GenericExtract; full-tier languages
// (Go, Python, TypeScript, …) build a ParseSpec directly so they can keep
// their bespoke symbol/import/call logic and export rules.
type ParseSpec struct {
	// Lang and Query are already resolved by the caller (TypeScript, for
	// instance, picks the TS vs TSX grammar before calling).
	Lang  *sitter.Language
	Query *sitter.Query

	// AbsorbSymbol handles a match carrying a symbol.def capture. It inserts
	// into out keyed by the def's start byte so repeated matches for the same
	// definition dedupe (or merge) rather than double-count. Required.
	AbsorbSymbol func(out map[uint32]*models.Symbol, caps map[string][]*sitter.Node, fi models.FileInfo, source []byte)

	// AbsorbImport and AbsorbCall convert a match into an import/call, or
	// return nil to skip it. Either may be nil when a language captures none.
	AbsorbImport func(caps map[string][]*sitter.Node, source []byte) *models.Import
	AbsorbCall   func(caps map[string][]*sitter.Node, source []byte) *models.CallSite

	// Finalize runs after symbols are collected, sorted, and call sites are
	// resolved. It populates language-specific outputs (Exports, Docstring)
	// and may post-process symbols — e.g. mark exports discovered by a
	// separate tree walk. Optional; when nil the result has no exports.
	Finalize func(root *sitter.Node, source []byte, pf *models.ParsedFile)
}

// RunQuery executes the standard tree-sitter extraction shell for spec and
// returns the parsed symbols, imports, and calls. It is the single
// implementation of the parse → query → dispatch → collect → resolve flow that
// every per-language parser used to duplicate.
func RunQuery(ctx context.Context, fi models.FileInfo, source []byte, spec ParseSpec) (models.ParsedFile, error) {
	if err := ctx.Err(); err != nil {
		return models.ParsedFile{}, err
	}

	tsp := sitter.NewParser()
	tsp.SetLanguage(spec.Lang)
	tree, err := tsp.ParseCtx(ctx, nil, source)
	if err != nil {
		return models.ParsedFile{}, fmt.Errorf("tree-sitter parse: %w", err)
	}
	defer tree.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(spec.Query, tree.RootNode())

	acc := matchAccumulator{symbols: map[uint32]*models.Symbol{}}
	for {
		m, ok := cursor.NextMatch()
		if !ok {
			break
		}
		acc.dispatch(BucketCaptures(m, spec.Query), fi, source, spec)
	}

	symbolsByStart := acc.symbols
	imports := acc.imports
	calls := acc.calls

	symbols := make([]models.Symbol, 0, len(symbolsByStart))
	for _, s := range symbolsByStart {
		symbols = append(symbols, *s)
	}
	SortByStartLine(symbols)
	for i := range calls {
		calls[i].CallerSymbolID = EnclosingSymbolID(symbols, calls[i].Line)
	}

	pf := models.ParsedFile{Symbols: symbols, Imports: imports, Calls: calls}
	if spec.Finalize != nil {
		spec.Finalize(tree.RootNode(), source, &pf)
	}
	return pf, nil
}

// matchAccumulator collects the symbols, imports, and calls produced as
// RunQuery walks query matches. Pulling the per-match dispatch onto a method
// keeps RunQuery itself a flat, readable sequence.
type matchAccumulator struct {
	symbols map[uint32]*models.Symbol
	imports []models.Import
	calls   []models.CallSite
}

// dispatch routes one match's captures to the matching absorber on spec.
func (a *matchAccumulator) dispatch(caps map[string][]*sitter.Node, fi models.FileInfo, source []byte, spec ParseSpec) {
	switch {
	case len(caps["symbol.def"]) > 0:
		spec.AbsorbSymbol(a.symbols, caps, fi, source)
	case len(caps["import.statement"]) > 0:
		if spec.AbsorbImport != nil {
			if im := spec.AbsorbImport(caps, source); im != nil {
				a.imports = append(a.imports, *im)
			}
		}
	case len(caps["call.site"]) > 0:
		if spec.AbsorbCall != nil {
			if cs := spec.AbsorbCall(caps, source); cs != nil {
				a.calls = append(a.calls, *cs)
			}
		}
	}
}

// ExportsByFlag returns the names of symbols whose IsExportedSymbol flag is
// set. Used by languages with an explicit export marker (Go's capitalization,
// JS/TS `export`).
func ExportsByFlag(symbols []models.Symbol) []string {
	exports := make([]string, 0)
	for _, s := range symbols {
		if s.IsExportedSymbol {
			exports = append(exports, s.Name)
		}
	}
	return exports
}

// ExportsPublicTopLevel returns the names of public, top-level symbols (no
// parent container). This is the export rule for languages where visibility
// keywords (or naming conventions mapped to visibility) determine the public
// API: Python, Java, C#, Rust, and the partial-tier languages.
func ExportsPublicTopLevel(symbols []models.Symbol) []string {
	exports := make([]string, 0)
	for _, s := range symbols {
		if s.Visibility == models.VisibilityPublic && s.ParentName == nil {
			exports = append(exports, s.Name)
		}
	}
	return exports
}
