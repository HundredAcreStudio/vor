package graph

import (
	"path"

	"github.com/HundredAcreStudio/vor/internal/ingestion/graph/resolver"
	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

// Builder turns a stream of ParsedFile into a populated *Graph. Use it in
// two phases: AddFile() per parsed file (no edges yet), then Build() to
// resolve imports and calls into edges.
//
// The two-phase shape lets us complete file discovery before any cross-file
// resolution runs — important because both imports and calls reach across
// files. Mirrors the Python `GraphBuilder.add_file` / `build` split.
type Builder struct {
	g       *Graph
	parsed  []models.ParsedFile
	byPath  map[string]*models.ParsedFile
	options Options
}

// Options tweaks Builder behaviour.
type Options struct {
	// MinCallConfidence drops call edges whose resolution confidence falls
	// below this value. Default 0.0 (keep everything).
	MinCallConfidence float64

	// ResolverContext carries language-specific data the per-language
	// import resolvers consume (Go module path from go.mod, Rust crate
	// name from Cargo.toml, etc.). The Files / FilesByDir fields are
	// populated by the builder; callers only need to set the
	// language-specific ones.
	ResolverContext resolver.Context
}

// NewBuilder constructs a Builder writing into g. The caller may pass an
// existing graph (e.g. to incrementally extend it) — typically nil/New().
func NewBuilder(g *Graph, opts Options) *Builder {
	if g == nil {
		g = New()
	}
	return &Builder{g: g, byPath: map[string]*models.ParsedFile{}, options: opts}
}

// AddFile registers a parsed file with the builder. Symbol and file nodes
// are created immediately; cross-file edges (imports, calls) are deferred
// to Build.
func (b *Builder) AddFile(parsed models.ParsedFile) {
	pCopy := parsed
	b.parsed = append(b.parsed, pCopy)
	b.byPath[parsed.FileInfo.Path] = &b.parsed[len(b.parsed)-1]

	fileNode := b.g.AddFileNode(parsed.FileInfo)
	fileNode.SymbolCount = len(parsed.Symbols)
	if len(parsed.ParseErrors) > 0 {
		fileNode.HasError = true
	}

	// Symbol nodes + defines/has_method edges.
	for _, sym := range parsed.Symbols {
		symNode := b.g.AddSymbolNode(parsed.FileInfo.Path, sym)
		b.g.AddEdge(fileNode, symNode, models.EdgeDefines, 1.0, nil)

		// has_method / has_property: edge from parent class symbol to this
		// method/property symbol when both exist within the same file.
		if sym.ParentName != nil {
			parentID := parsed.FileInfo.Path + "::" + *sym.ParentName
			if parent := b.g.LookupNode(parentID); parent != nil {
				edgeType := models.EdgeHasMethod
				if sym.Kind == models.KindVariable || sym.Kind == models.KindConstant {
					edgeType = models.EdgeHasProperty
				}
				b.g.AddEdge(parent, symNode, edgeType, 1.0, nil)
			}
		}
	}
}

// Build resolves imports and calls across all added files into edges.
// Safe to call multiple times (it is idempotent because AddEdge dedupes).
func (b *Builder) Build() *Graph {
	b.resolveImports()
	b.resolveCalls()
	return b.g
}

// resolveImports walks every parsed file's imports and dispatches each
// one to the per-language resolver registered in
// internal/ingestion/graph/resolver. Languages without a registered
// resolver produce no graph edges — by design, since wrong edges are
// worse than absent ones for dead-code and hidden_coupling analysis.
func (b *Builder) resolveImports() {
	// Materialise the resolver context once per Build() call. Caller-
	// supplied language-specific fields (GoModulePath, RustCrateName, ...)
	// flow through; the Files / FilesByDir indexes are derived from
	// the parsed set.
	ctx := b.options.ResolverContext
	ctx.Files = make(map[string]bool, len(b.byPath))
	ctx.FilesByDir = map[string][]string{}
	for p := range b.byPath {
		ctx.Files[p] = true
		dir := path.Dir(p)
		ctx.FilesByDir[dir] = append(ctx.FilesByDir[dir], p)
	}

	for i := range b.parsed {
		parsed := &b.parsed[i]
		fileNode := b.g.LookupNode(parsed.FileInfo.Path)
		if fileNode == nil {
			continue
		}
		r := resolver.Lookup(parsed.FileInfo.Language)
		if r == nil {
			continue // no resolver for this language — skip
		}
		for _, imp := range parsed.Imports {
			for _, targetPath := range r.Resolve(parsed.FileInfo, imp, ctx) {
				if targetPath == parsed.FileInfo.Path {
					continue // self-import / same-package
				}
				targetNode := b.g.LookupNode(targetPath)
				if targetNode == nil {
					continue
				}
				b.g.AddEdge(fileNode, targetNode, models.EdgeImports, 1.0, imp.ImportedNames)
			}
		}
	}
}

// resolveCalls walks every parsed file's calls and emits `calls` edges
// using the 3-tier resolver:
//
//	Tier 1 — name matches a symbol in the same file (confidence 1.0)
//	Tier 2 — receiver matches an imported file; symbol exists there (0.7)
//	Tier 3 — name matches a symbol in any file (low confidence 0.3)
//
// Only the caller's enclosing symbol contributes an edge — top-level
// calls have no edge source (they're implicit module-scope code).
func (b *Builder) resolveCalls() {
	// Index symbols by simple name across the whole repo for Tier 3.
	symbolsByName := map[string][]*Node{}
	// Index per-file (importer path -> imported file paths) for Tier 2.
	importsByFile := map[string][]string{}
	for _, node := range b.g.Nodes() {
		if node.Kind != NodeSymbol {
			continue
		}
		symbolsByName[node.Symbol.Name] = append(symbolsByName[node.Symbol.Name], node)
	}
	for _, e := range b.g.Edges() {
		if e.Type != models.EdgeImports {
			continue
		}
		importsByFile[e.F.StringID] = append(importsByFile[e.F.StringID], e.T.StringID)
	}

	for i := range b.parsed {
		parsed := &b.parsed[i]
		// Build a per-file symbol-name index for Tier 1.
		localSymbols := map[string]*Node{}
		for _, sym := range parsed.Symbols {
			n := b.g.LookupNode(sym.ID)
			if n != nil {
				localSymbols[sym.Name] = n
			}
		}

		for _, call := range parsed.Calls {
			if call.CallerSymbolID == nil {
				continue
			}
			caller := b.g.LookupNode(*call.CallerSymbolID)
			if caller == nil {
				continue
			}

			var (
				target     *Node
				confidence float64
			)

			// Tier 1: same-file symbol.
			if local, ok := localSymbols[call.TargetName]; ok && local != caller {
				target = local
				confidence = 1.0
			}

			// Tier 2: receiver matches an imported file's symbol.
			if target == nil && call.ReceiverName != nil {
				for _, importedPath := range importsByFile[parsed.FileInfo.Path] {
					candidateID := importedPath + "::" + call.TargetName
					if n := b.g.LookupNode(candidateID); n != nil {
						target = n
						confidence = 0.7
						break
					}
				}
			}

			// Tier 3: name match anywhere.
			if target == nil {
				if candidates := symbolsByName[call.TargetName]; len(candidates) > 0 {
					// Pick the first stable candidate. A smarter heuristic
					// (e.g. closest path) is the natural future refinement.
					target = candidates[0]
					confidence = 0.3
				}
			}

			if target == nil {
				continue
			}
			if confidence < b.options.MinCallConfidence {
				continue
			}
			b.g.AddEdge(caller, target, models.EdgeCalls, confidence, nil)
		}
	}
}
