package common

import (
	"context"
	"fmt"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// GenericConfig drives GenericExtract for languages whose extraction
// follows the standard capture-name contract (symbol.def / symbol.name /
// symbol.params / symbol.modifiers / import.statement / import.module /
// import.kind / call.site / call.target / call.receiver / call.arguments).
//
// The Partial- and Traversal-tier languages (Ruby, PHP, Swift, Kotlin,
// Scala, Lua/Luau) share this skeleton; each supplies only its grammar,
// a kind mapping, and a few flags. Full-tier languages with heritage and
// binding analysis (Go, Python, …) keep their bespoke parsers.
type GenericConfig struct {
	Lang      *sitter.Language
	QueryName string
	LangTag   models.LanguageTag

	// Query-compile cache, held as package vars by the calling parser.
	Once     *sync.Once
	QueryPtr **sitter.Query
	ErrPtr   *error

	// KindFor maps a symbol.def node to a SymbolKind.
	KindFor func(def *sitter.Node, source []byte) models.SymbolKind

	// DefaultVis is the visibility assigned before modifiers are applied.
	// Languages with no access keywords (Lua, Ruby) use public so their
	// top-level symbols count as exports.
	DefaultVis models.Visibility

	// ContainerTypes are node types that act as a parent scope for nested
	// functions/methods (e.g. "class", "class_declaration", "module").
	// When a function/method def has such an ancestor, it becomes a method
	// with that ancestor's name as ParentName.
	ContainerTypes map[string]struct{}

	// MethodKinds limits which kinds get re-tagged as methods when nested in
	// a container. Defaults to {function} when nil.
	MethodKinds map[models.SymbolKind]struct{}

	// BranchTypes are decision-point node types for the cyclomatic estimate.
	BranchTypes map[string]struct{}

	// ImportKinds, when non-nil, switches imports to "require-style": an
	// import.statement counts only when its import.kind capture text is in
	// this set (e.g. {"require","require_relative"} for Ruby). Calls whose
	// target is in this set are then skipped (they are imports, not calls).
	ImportKinds map[string]struct{}

	// RelativePrefixes mark a module path as a relative import.
	RelativePrefixes []string
}

// GenericExtract runs the standard tree-sitter extraction loop for cfg and
// returns the parsed symbols, imports, and calls.
func GenericExtract(ctx context.Context, fi models.FileInfo, source []byte, cfg GenericConfig) (models.ParsedFile, error) {
	if err := ctx.Err(); err != nil {
		return models.ParsedFile{}, err
	}
	tsp := sitter.NewParser()
	tsp.SetLanguage(cfg.Lang)
	tree, err := tsp.ParseCtx(ctx, nil, source)
	if err != nil {
		return models.ParsedFile{}, fmt.Errorf("%s: tree-sitter parse: %w", cfg.LangTag, err)
	}
	defer tree.Close()

	query, err := LoadQueryOnce(cfg.QueryName, cfg.Lang, cfg.Once, cfg.QueryPtr, cfg.ErrPtr)
	if err != nil {
		return models.ParsedFile{}, err
	}

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, tree.RootNode())

	methodKinds := cfg.MethodKinds
	if methodKinds == nil {
		methodKinds = map[models.SymbolKind]struct{}{models.KindFunction: {}}
	}

	symbolsByStart := map[uint32]*models.Symbol{}
	var imports []models.Import
	var calls []models.CallSite

	for {
		m, ok := cursor.NextMatch()
		if !ok {
			break
		}
		caps := BucketCaptures(m, query)
		switch {
		case len(caps["symbol.def"]) > 0 && len(caps["symbol.name"]) > 0:
			absorbGenericSymbol(symbolsByStart, caps, fi, source, cfg, methodKinds)
		case len(caps["import.statement"]) > 0:
			if im := absorbGenericImport(caps, source, cfg); im != nil {
				imports = append(imports, *im)
			}
		case len(caps["call.site"]) > 0 && len(caps["call.target"]) > 0:
			if cs := absorbGenericCall(caps, source, cfg); cs != nil {
				calls = append(calls, *cs)
			}
		}
	}

	symbols := make([]models.Symbol, 0, len(symbolsByStart))
	for _, s := range symbolsByStart {
		symbols = append(symbols, *s)
	}
	SortByStartLine(symbols)
	for i := range calls {
		calls[i].CallerSymbolID = EnclosingSymbolID(symbols, calls[i].Line)
	}

	exports := make([]string, 0)
	for _, s := range symbols {
		if s.Visibility == models.VisibilityPublic && s.ParentName == nil {
			exports = append(exports, s.Name)
		}
	}

	return models.ParsedFile{
		Symbols: symbols,
		Imports: imports,
		Calls:   calls,
		Exports: exports,
	}, nil
}

func absorbGenericSymbol(out map[uint32]*models.Symbol, caps map[string][]*sitter.Node, fi models.FileInfo, source []byte, cfg GenericConfig, methodKinds map[models.SymbolKind]struct{}) {
	def := caps["symbol.def"][0]
	name := strings.TrimSpace(caps["symbol.name"][0].Content(source))
	if name == "" {
		return
	}
	start := def.StartByte()
	if existing, exists := out[start]; exists {
		applyModifiers(existing, caps, source)
		return
	}

	kind := cfg.KindFor(def, source)
	sym := &models.Symbol{
		Name:          name,
		Kind:          kind,
		StartLine:     int(def.StartPoint().Row) + 1,
		EndLine:       int(def.EndPoint().Row) + 1,
		Visibility:    cfg.DefaultVis,
		Language:      string(cfg.LangTag),
		QualifiedName: name,
	}
	if _, isFn := methodKinds[kind]; isFn {
		sym.ComplexityEstimate = 1 + CountBranchNodes(def, cfg.BranchTypes)
		sym.NestingDepth = MaxNestingDepth(def, cfg.BranchTypes)
	} else {
		sym.ComplexityEstimate = 1
	}
	applyModifiers(sym, caps, source)

	if _, canBeMethod := methodKinds[kind]; canBeMethod {
		if parent, ok := EnclosingContainerName(def, cfg.ContainerTypes, source); ok {
			sym.Kind = models.KindMethod
			sym.ParentName = PtrStr(parent)
			sym.ID = fmt.Sprintf("%s::%s::%s", fi.Path, parent, name)
			sym.QualifiedName = parent + "." + name
		}
	}
	if sym.ID == "" {
		sym.ID = fmt.Sprintf("%s::%s", fi.Path, name)
	}
	sym.IsExportedSymbol = sym.Visibility == models.VisibilityPublic
	sym.Signature = strings.TrimSpace(SignatureSlice(source, def, caps["symbol.params"]))
	out[start] = sym
}

// applyModifiers upgrades visibility from any symbol.modifiers capture text.
func applyModifiers(sym *models.Symbol, caps map[string][]*sitter.Node, source []byte) {
	for _, m := range caps["symbol.modifiers"] {
		switch strings.TrimSpace(strings.ToLower(m.Content(source))) {
		case "public":
			sym.Visibility = models.VisibilityPublic
		case "private":
			sym.Visibility = models.VisibilityPrivate
		case "protected":
			sym.Visibility = models.VisibilityProtected
		case "internal":
			sym.Visibility = models.VisibilityInternal
		default:
			// Rust-style "pub", "pub(crate)" etc.
			if strings.HasPrefix(strings.ToLower(m.Content(source)), "pub") {
				sym.Visibility = models.VisibilityPublic
			}
		}
	}
}

func absorbGenericImport(caps map[string][]*sitter.Node, source []byte, cfg GenericConfig) *models.Import {
	if cfg.ImportKinds != nil {
		kinds := caps["import.kind"]
		if len(kinds) == 0 {
			return nil
		}
		if _, ok := cfg.ImportKinds[strings.TrimSpace(kinds[0].Content(source))]; !ok {
			return nil
		}
	}
	mods := caps["import.module"]
	if len(mods) == 0 {
		return nil
	}
	module := strings.TrimSpace(mods[0].Content(source))
	module = strings.Trim(module, "\"'`")
	module = stripImportKeyword(module)
	raw := module
	if stmt := caps["import.statement"]; len(stmt) > 0 {
		raw = strings.TrimSpace(stmt[0].Content(source))
	}
	return &models.Import{
		RawStatement:  raw,
		ModulePath:    module,
		ImportedNames: []string{},
		IsRelative:    isRelative(module, cfg.RelativePrefixes),
	}
}

func absorbGenericCall(caps map[string][]*sitter.Node, source []byte, cfg GenericConfig) *models.CallSite {
	target := caps["call.target"][0]
	name := target.Content(source)
	if cfg.ImportKinds != nil {
		if _, isImport := cfg.ImportKinds[name]; isImport {
			return nil // require-style call already captured as an import
		}
	}
	cs := models.CallSite{
		TargetName: name,
		Line:       int(target.StartPoint().Row) + 1,
	}
	if recv := caps["call.receiver"]; len(recv) > 0 {
		cs.ReceiverName = PtrStr(recv[0].Content(source))
	}
	if args := caps["call.arguments"]; len(args) > 0 {
		n := int(args[0].NamedChildCount())
		cs.ArgumentCount = &n
	}
	return &cs
}

// EnclosingContainerName walks def's ancestors for the nearest node whose
// type is in containerTypes and returns its name. The name is taken from a
// "name" field if present, else the first identifier-like named child.
func EnclosingContainerName(def *sitter.Node, containerTypes map[string]struct{}, source []byte) (string, bool) {
	if containerTypes == nil {
		return "", false
	}
	for n := def.Parent(); n != nil; n = n.Parent() {
		if _, ok := containerTypes[n.Type()]; !ok {
			continue
		}
		if nameNode := n.ChildByFieldName("name"); nameNode != nil {
			return nameNode.Content(source), true
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			switch c.Type() {
			case "identifier", "type_identifier", "constant", "name", "simple_identifier":
				return c.Content(source), true
			}
		}
		return "", false
	}
	return "", false
}

// stripImportKeyword removes a leading import keyword from a module string
// captured from a whole import statement (e.g. Scala's
// "import scala.collection.mutable"). Harmless when no keyword is present.
func stripImportKeyword(module string) string {
	for _, kw := range []string{"import ", "use ", "from ", "require ", "package ", "namespace "} {
		if rest, ok := strings.CutPrefix(module, kw); ok {
			return strings.TrimSpace(rest)
		}
	}
	return module
}

func isRelative(module string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(module, p) {
			return true
		}
	}
	return false
}
