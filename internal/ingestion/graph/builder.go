package graph

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/repowise-dev/repowise-go/internal/ingestion/languages"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
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

// resolveImports walks every parsed file's imports and attempts to map the
// module path to another file in the repo. Three strategies, in order:
//
//  1. Relative imports (./foo, ../bar) are resolved against the importer's
//     directory, trying each registered extension for the importer's
//     language plus common ones (index.{ts,tsx,js,py}).
//  2. Python dotted imports ("pkg.sub.mod") are resolved by treating "." as
//     "/" and appending ".py" / "/__init__.py".
//  3. Best-effort suffix match across all known files.
//
// Anything that fails to resolve becomes a no-op (no edge). The Python
// implementation likewise drops un-resolvable imports rather than emitting
// edges to placeholder external nodes.
func (b *Builder) resolveImports() {
	for i := range b.parsed {
		parsed := &b.parsed[i]
		fileNode := b.g.LookupNode(parsed.FileInfo.Path)
		if fileNode == nil {
			continue
		}
		for _, imp := range parsed.Imports {
			targetPath, ok := b.resolveImportPath(parsed.FileInfo, imp)
			if !ok {
				continue
			}
			targetNode := b.g.LookupNode(targetPath)
			if targetNode == nil {
				continue
			}
			b.g.AddEdge(fileNode, targetNode, models.EdgeImports, 1.0, imp.ImportedNames)
		}
	}
}

func (b *Builder) resolveImportPath(fi models.FileInfo, imp models.Import) (string, bool) {
	module := strings.TrimSpace(imp.ModulePath)
	if module == "" {
		return "", false
	}
	importerDir := path.Dir(fi.Path)

	// Strategy 1: relative import.
	if strings.HasPrefix(module, ".") {
		candidates := relativeImportCandidates(importerDir, module, fi.Language)
		for _, c := range candidates {
			if _, ok := b.byPath[c]; ok {
				return c, true
			}
		}
		return "", false
	}

	// Strategy 2: Python dotted import.
	if fi.Language == "python" {
		dotted := strings.ReplaceAll(module, ".", "/")
		candidates := []string{
			dotted + ".py",
			dotted + "/__init__.py",
		}
		for _, c := range candidates {
			if _, ok := b.byPath[c]; ok {
				return c, true
			}
		}
	}

	// Strategy 3: TypeScript / JavaScript bare specifiers that happen to
	// match a file (e.g. tsconfig path aliases not yet plumbed through
	// here, or just naive matches). Suffix match.
	for p := range b.byPath {
		if strings.HasSuffix(p, "/"+module) {
			return p, true
		}
		// Strip extensions in path to handle `import { x } from "calc"`
		// matching "calc.ts".
		ext := filepath.Ext(p)
		if ext != "" && strings.HasSuffix(strings.TrimSuffix(p, ext), "/"+module) {
			return p, true
		}
	}
	return "", false
}

// relativeImportCandidates expands "./calc" / "../utils" into a candidate
// list of possible target paths. The language drives which extensions are
// considered.
func relativeImportCandidates(importerDir, module string, lang models.LanguageTag) []string {
	joined := path.Join(importerDir, module)
	exts := relativeImportExts(lang)
	out := make([]string, 0, len(exts)*2+1)
	out = append(out, joined) // exact filename if user wrote one
	for _, ext := range exts {
		out = append(out, joined+ext)
	}
	for _, ext := range exts {
		out = append(out, joined+"/index"+ext)
	}
	return out
}

func relativeImportExts(lang models.LanguageTag) []string {
	spec := languages.Lookup(lang)
	if spec != nil && len(spec.Extensions) > 0 {
		return append([]string(nil), spec.Extensions...)
	}
	return []string{".ts", ".tsx", ".js", ".py", ".go"}
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
