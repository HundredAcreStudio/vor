// Package python is the Python-language parser. Wraps tree-sitter-python via
// smacker/go-tree-sitter and applies Python-specific rules: underscore-based
// visibility, decorator-derived metadata, async detection, and class-based
// parent extraction.
package python

import (
	"context"
	"fmt"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser/common"
)

const langTag models.LanguageTag = "python"

var (
	compiledQuery     *sitter.Query
	compiledQueryOnce sync.Once
	compiledQueryErr  error
)

func loadQuery() (*sitter.Query, error) {
	return common.LoadQueryOnce("python.scm", python.GetLanguage(), &compiledQueryOnce, &compiledQuery, &compiledQueryErr)
}

// Parser is the registered Python parser.
type Parser struct{}

func init() { parser.Register(langTag, &Parser{}) }

// Parse extracts Python symbols, imports, and calls from source.
func (p *Parser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	if err := ctx.Err(); err != nil {
		return models.ParsedFile{}, err
	}

	tsp := sitter.NewParser()
	tsp.SetLanguage(python.GetLanguage())
	tree, err := tsp.ParseCtx(ctx, nil, source)
	if err != nil {
		return models.ParsedFile{}, fmt.Errorf("tree-sitter parse: %w", err)
	}
	defer tree.Close()

	query, err := loadQuery()
	if err != nil {
		return models.ParsedFile{}, err
	}

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(query, tree.RootNode())

	symbolsByDefStart := map[uint32]*models.Symbol{}
	var imports []models.Import
	var calls []models.CallSite

	for {
		m, ok := cursor.NextMatch()
		if !ok {
			break
		}
		caps := common.BucketCaptures(m, query)
		switch {
		case len(caps["symbol.def"]) > 0:
			absorbSymbol(symbolsByDefStart, caps, fi, source)
		case len(caps["import.statement"]) > 0:
			if im := absorbImport(caps, source); im != nil {
				imports = append(imports, *im)
			}
		case len(caps["call.site"]) > 0:
			if cs := absorbCall(caps, source); cs != nil {
				calls = append(calls, *cs)
			}
		}
	}

	symbols := make([]models.Symbol, 0, len(symbolsByDefStart))
	for _, s := range symbolsByDefStart {
		symbols = append(symbols, *s)
	}
	common.SortByStartLine(symbols)

	for i := range calls {
		calls[i].CallerSymbolID = common.EnclosingSymbolID(symbols, calls[i].Line)
	}

	// Exports for Python: public symbols (those not starting with "_") at
	// module level. Symbols inside classes aren't exports per se, but
	// repowise mirrors the Python source which lists everything public.
	exports := make([]string, 0)
	for _, s := range symbols {
		if s.Visibility == models.VisibilityPublic && s.ParentName == nil {
			exports = append(exports, s.Name)
		}
	}

	docstring := extractModuleDocstring(tree.RootNode(), source)
	return models.ParsedFile{
		Symbols:   symbols,
		Imports:   imports,
		Calls:     calls,
		Exports:   exports,
		Docstring: docstring,
	}, nil
}

func absorbSymbol(out map[uint32]*models.Symbol, caps map[string][]*sitter.Node, fi models.FileInfo, source []byte) {
	def := caps["symbol.def"][0]
	nameNodes := caps["symbol.name"]
	if len(nameNodes) == 0 {
		return
	}
	name := nameNodes[0].Content(source)

	startByte := def.StartByte()
	if _, exists := out[startByte]; exists {
		// Decorated and bare matches can both hit the same def node — first
		// wins, decorator pass merges into it below if needed.
		mergeDecorator(out[startByte], caps, source)
		return
	}

	complexity := 1
	if def.Type() == "function_definition" {
		complexity = 1 + common.CountBranchNodes(def, pythonBranchNodeTypes)
	}

	sym := &models.Symbol{
		Name:               name,
		Kind:               kindForNode(def),
		StartLine:          int(def.StartPoint().Row) + 1,
		EndLine:            int(def.EndPoint().Row) + 1,
		Visibility:         pythonVisibility(name),
		Language:           string(langTag),
		ComplexityEstimate: complexity,
		IsExportedSymbol:   pythonVisibility(name) == models.VisibilityPublic,
		QualifiedName:      name,
		IsAsync:            isAsync(def),
	}

	// Methods: walk up looking for a class_definition ancestor.
	if parentName, isMethod := enclosingClassName(def, source); isMethod {
		sym.Kind = models.KindMethod
		sym.ParentName = common.PtrStr(parentName)
		sym.ID = fmt.Sprintf("%s::%s::%s", fi.Path, parentName, name)
		sym.QualifiedName = parentName + "." + name
	} else {
		sym.ID = fmt.Sprintf("%s::%s", fi.Path, name)
	}

	sym.Signature = strings.TrimSpace(common.SignatureSlice(source, def, caps["symbol.params"]))
	mergeDecorator(sym, caps, source)
	out[startByte] = sym
}

func mergeDecorator(sym *models.Symbol, caps map[string][]*sitter.Node, source []byte) {
	for _, m := range caps["symbol.modifiers"] {
		text := strings.TrimSpace(m.Content(source))
		if text == "" {
			continue
		}
		sym.Decorators = append(sym.Decorators, text)
	}
}

func absorbImport(caps map[string][]*sitter.Node, source []byte) *models.Import {
	stmt := caps["import.statement"][0]
	modules := caps["import.module"]
	if len(modules) == 0 {
		return nil
	}
	raw := stmt.Content(source)
	module := strings.TrimSpace(modules[0].Content(source))
	// `from . import x` style produces a relative_import node whose text is
	// just the dots. The actual module path is empty in that case.
	isRelative := strings.HasPrefix(raw, "from .")
	return &models.Import{
		RawStatement:  raw,
		ModulePath:    module,
		ImportedNames: []string{},
		IsRelative:    isRelative,
	}
}

func absorbCall(caps map[string][]*sitter.Node, source []byte) *models.CallSite {
	target := caps["call.target"]
	if len(target) == 0 {
		return nil
	}
	cs := models.CallSite{
		TargetName: target[0].Content(source),
		Line:       int(target[0].StartPoint().Row) + 1,
	}
	if recv := caps["call.receiver"]; len(recv) > 0 {
		cs.ReceiverName = common.PtrStr(recv[0].Content(source))
	}
	if args := caps["call.arguments"]; len(args) > 0 {
		n := int(args[0].NamedChildCount())
		cs.ArgumentCount = &n
	}
	return &cs
}

// pythonBranchNodeTypes: tree-sitter-python decision-point nodes for
// McCabe cyclomatic complexity. case_clause covers match/case;
// except_clause covers try/except branches. Ternaries (a if b else c)
// also count.
var pythonBranchNodeTypes = map[string]struct{}{
	"if_statement":           {},
	"elif_clause":            {},
	"for_statement":          {},
	"while_statement":        {},
	"except_clause":          {},
	"case_clause":            {},
	"conditional_expression": {},
}

// ---- Python-specific rules -------------------------------------------------

func kindForNode(n *sitter.Node) models.SymbolKind {
	switch n.Type() {
	case "function_definition":
		return models.KindFunction
	case "class_definition":
		return models.KindClass
	default:
		return models.KindFunction
	}
}

// pythonVisibility applies the underscore convention:
//
//	__dunder__ -> public (Python magic methods)
//	__name    -> private (name-mangled)
//	_name     -> protected (convention)
//	name      -> public
func pythonVisibility(name string) models.Visibility {
	switch {
	case strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__"):
		return models.VisibilityPublic
	case strings.HasPrefix(name, "__"):
		return models.VisibilityPrivate
	case strings.HasPrefix(name, "_"):
		return models.VisibilityProtected
	default:
		return models.VisibilityPublic
	}
}

// isAsync returns true when the definition starts with `async`. tree-sitter-
// python represents `async def` as function_definition whose first child is
// the literal "async" keyword.
func isAsync(def *sitter.Node) bool {
	for i := 0; i < int(def.ChildCount()); i++ {
		child := def.Child(i)
		if child.Type() == "async" {
			return true
		}
		// Stop scanning once we reach the def keyword — no need to look
		// further.
		if child.Type() == "def" {
			return false
		}
	}
	return false
}

// enclosingClassName walks up def's ancestors looking for a class_definition.
// Returns the class name and true if found.
func enclosingClassName(def *sitter.Node, source []byte) (string, bool) {
	for n := def.Parent(); n != nil; n = n.Parent() {
		if n.Type() != "class_definition" {
			continue
		}
		nameNode := n.ChildByFieldName("name")
		if nameNode != nil {
			return nameNode.Content(source), true
		}
	}
	return "", false
}

// extractModuleDocstring returns the first string literal at module scope, if
// any. tree-sitter-python represents this as the first child of the
// module's first expression_statement.
func extractModuleDocstring(root *sitter.Node, source []byte) *string {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		stmt := root.NamedChild(i)
		if stmt.Type() != "expression_statement" {
			return nil
		}
		if stmt.NamedChildCount() == 0 {
			continue
		}
		first := stmt.NamedChild(0)
		if first.Type() != "string" {
			return nil
		}
		text := strings.Trim(first.Content(source), `"'`)
		text = strings.TrimSpace(text)
		if text == "" {
			return nil
		}
		return common.PtrStr(text)
	}
	return nil
}
