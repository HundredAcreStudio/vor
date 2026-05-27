// Package javascript is the JavaScript-language parser. Shares structure
// with the typescript subpackage but binds to tree-sitter-javascript
// instead. Handles plain .js / .mjs / .cjs / .jsx files (the TypeScript
// parser owns .ts / .tsx).
package javascript

import (
	"context"
	"fmt"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/javascript"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser/common"
)

const langTag models.LanguageTag = "javascript"

var (
	compiledQuery     *sitter.Query
	compiledQueryOnce sync.Once
	compiledQueryErr  error
)

func loadQuery() (*sitter.Query, error) {
	return common.LoadQueryOnce("javascript.scm", javascript.GetLanguage(),
		&compiledQueryOnce, &compiledQuery, &compiledQueryErr)
}

// Parser is the registered JavaScript parser.
type Parser struct{}

func init() { parser.Register(langTag, &Parser{}) }

// Parse extracts JavaScript symbols, imports, and calls (including JSX
// element references treated as calls to the component).
func (p *Parser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	if err := ctx.Err(); err != nil {
		return models.ParsedFile{}, err
	}

	tsp := sitter.NewParser()
	tsp.SetLanguage(javascript.GetLanguage())
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
	exportedNames := map[string]struct{}{}

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

	collectExportedNames(tree.RootNode(), source, exportedNames)
	for i := range symbolsByDefStart {
		if _, exported := exportedNames[symbolsByDefStart[i].Name]; exported {
			symbolsByDefStart[i].IsExportedSymbol = true
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

	exports := make([]string, 0, len(exportedNames))
	for name := range exportedNames {
		exports = append(exports, name)
	}

	return models.ParsedFile{
		Symbols: symbols,
		Imports: imports,
		Calls:   calls,
		Exports: exports,
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
		return
	}

	complexity, nesting := 1, 0
	switch def.Type() {
	case "function_declaration", "generator_function_declaration",
		"method_definition", "lexical_declaration":
		complexity = 1 + common.CountBranchNodes(def, jsBranchNodeTypes)
		nesting = common.MaxNestingDepth(def, jsBranchNodeTypes)
	}

	sym := &models.Symbol{
		Name:               name,
		Kind:               kindForNode(def),
		StartLine:          int(def.StartPoint().Row) + 1,
		EndLine:            int(def.EndPoint().Row) + 1,
		Visibility:         models.VisibilityPublic, // JS has no native access modifiers
		Language:           string(langTag),
		ComplexityEstimate: complexity,
		NestingDepth:       nesting,
		QualifiedName:      name,
	}

	if parentName, isMethod := enclosingClassName(def, source); isMethod {
		sym.Kind = models.KindMethod
		sym.ParentName = common.PtrStr(parentName)
		sym.ID = fmt.Sprintf("%s::%s::%s", fi.Path, parentName, name)
		sym.QualifiedName = parentName + "." + name
	} else {
		sym.ID = fmt.Sprintf("%s::%s", fi.Path, name)
	}

	sym.Signature = strings.TrimSpace(common.SignatureSlice(source, def, caps["symbol.params"]))
	out[startByte] = sym
}

func absorbImport(caps map[string][]*sitter.Node, source []byte) *models.Import {
	stmt := caps["import.statement"][0]
	modules := caps["import.module"]
	if len(modules) == 0 {
		return nil
	}
	raw := stmt.Content(source)
	module := strings.Trim(modules[0].Content(source), "'\"`")
	return &models.Import{
		RawStatement:  raw,
		ModulePath:    module,
		ImportedNames: []string{},
		IsRelative:    strings.HasPrefix(module, ".") || strings.HasPrefix(module, "/"),
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

// jsBranchNodeTypes — identical to the TS set; JS grammar uses the same
// names except for type-system-specific forms.
var jsBranchNodeTypes = map[string]struct{}{
	"if_statement":       {},
	"for_statement":      {},
	"for_in_statement":   {},
	"for_of_statement":   {},
	"while_statement":    {},
	"do_statement":       {},
	"switch_case":        {},
	"catch_clause":       {},
	"ternary_expression": {},
	"else_clause":        {},
}

func kindForNode(n *sitter.Node) models.SymbolKind {
	switch n.Type() {
	case "function_declaration", "generator_function_declaration", "lexical_declaration":
		return models.KindFunction
	case "class_declaration":
		return models.KindClass
	case "method_definition":
		return models.KindMethod
	default:
		return models.KindFunction
	}
}

func enclosingClassName(def *sitter.Node, source []byte) (string, bool) {
	for n := def.Parent(); n != nil; n = n.Parent() {
		if n.Type() == "class_declaration" {
			if nm := n.ChildByFieldName("name"); nm != nil {
				return nm.Content(source), true
			}
		}
	}
	return "", false
}

// collectExportedNames walks the module body for `export function/class/...`
// and `export { ... }` clauses, matching the TypeScript implementation.
func collectExportedNames(root *sitter.Node, source []byte, out map[string]struct{}) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		if child.Type() != "export_statement" {
			continue
		}
		if decl := child.ChildByFieldName("declaration"); decl != nil {
			collectDeclNames(decl, source, out)
			continue
		}
		for j := 0; j < int(child.NamedChildCount()); j++ {
			n := child.NamedChild(j)
			if n.Type() != "export_clause" {
				continue
			}
			for k := 0; k < int(n.NamedChildCount()); k++ {
				spec := n.NamedChild(k)
				if spec.Type() != "export_specifier" {
					continue
				}
				if nm := spec.ChildByFieldName("name"); nm != nil {
					out[nm.Content(source)] = struct{}{}
				}
			}
		}
	}
}

func collectDeclNames(decl *sitter.Node, source []byte, out map[string]struct{}) {
	switch decl.Type() {
	case "function_declaration", "generator_function_declaration", "class_declaration":
		if nm := decl.ChildByFieldName("name"); nm != nil {
			out[nm.Content(source)] = struct{}{}
		}
	case "lexical_declaration", "variable_declaration":
		for i := 0; i < int(decl.NamedChildCount()); i++ {
			d := decl.NamedChild(i)
			if d.Type() == "variable_declarator" {
				if nm := d.ChildByFieldName("name"); nm != nil && nm.Type() == "identifier" {
					out[nm.Content(source)] = struct{}{}
				}
			}
		}
	}
}
