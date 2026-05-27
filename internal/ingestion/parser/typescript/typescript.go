// Package typescript is the TypeScript/TSX parser. It registers two parsers
// against the language registry — one for plain TypeScript files (.ts) and
// one for TSX (.tsx) — both running the same typescript.scm query (TSX uses
// the JSX-aware grammar variant so JSX-embedded expressions parse).
package typescript

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser/common"
)

const langTag models.LanguageTag = "typescript"

// We keep one compiled query per grammar variant — the parsers reuse the
// same .scm but tree-sitter requires the query to be compiled against the
// specific language object.
var (
	tsQuery     *sitter.Query
	tsQueryOnce sync.Once
	tsQueryErr  error

	tsxQuery     *sitter.Query
	tsxQueryOnce sync.Once
	tsxQueryErr  error
)

func loadTSQuery() (*sitter.Query, error) {
	return common.LoadQueryOnce("typescript.scm", typescript.GetLanguage(), &tsQueryOnce, &tsQuery, &tsQueryErr)
}

func loadTSXQuery() (*sitter.Query, error) {
	return common.LoadQueryOnce("typescript.scm", tsx.GetLanguage(), &tsxQueryOnce, &tsxQuery, &tsxQueryErr)
}

// Parser handles TypeScript files. Decides on TS vs TSX at Parse time based
// on the file extension so callers don't need to pre-route.
type Parser struct{}

func init() { parser.Register(langTag, &Parser{}) }

// Parse extracts TypeScript symbols, imports, and calls.
func (p *Parser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	if err := ctx.Err(); err != nil {
		return models.ParsedFile{}, err
	}

	isTSX := strings.EqualFold(filepath.Ext(fi.Path), ".tsx")

	var (
		lang  *sitter.Language
		query *sitter.Query
		err   error
	)
	if isTSX {
		lang = tsx.GetLanguage()
		query, err = loadTSXQuery()
	} else {
		lang = typescript.GetLanguage()
		query, err = loadTSQuery()
	}
	if err != nil {
		return models.ParsedFile{}, err
	}

	tsp := sitter.NewParser()
	tsp.SetLanguage(lang)
	tree, err := tsp.ParseCtx(ctx, nil, source)
	if err != nil {
		return models.ParsedFile{}, fmt.Errorf("tree-sitter parse: %w", err)
	}
	defer tree.Close()

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

	// Walk the top level once to collect `export` markers, since the .scm
	// query doesn't capture them directly. Anything under an
	// export_statement at module level is publicly exported.
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
	if existing, exists := out[startByte]; exists {
		mergeModifiers(existing, caps, source)
		return
	}

	sym := &models.Symbol{
		Name:               name,
		Kind:               kindForNode(def),
		StartLine:          int(def.StartPoint().Row) + 1,
		EndLine:            int(def.EndPoint().Row) + 1,
		Visibility:         models.VisibilityPublic, // default; refined below
		Language:           string(langTag),
		ComplexityEstimate: 1,
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

	mergeModifiers(sym, caps, source)
	sym.Signature = strings.TrimSpace(common.SignatureSlice(source, def, caps["symbol.params"]))
	out[startByte] = sym
}

func mergeModifiers(sym *models.Symbol, caps map[string][]*sitter.Node, source []byte) {
	for _, m := range caps["symbol.modifiers"] {
		text := strings.TrimSpace(m.Content(source))
		switch text {
		case "private":
			sym.Visibility = models.VisibilityPrivate
		case "protected":
			sym.Visibility = models.VisibilityProtected
		case "public":
			sym.Visibility = models.VisibilityPublic
		default:
			// Decorator-ish modifiers (e.g., @Component) — store as decorators.
			if strings.HasPrefix(text, "@") {
				sym.Decorators = append(sym.Decorators, text)
			}
		}
	}
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

// ---- TypeScript-specific rules ---------------------------------------------

func kindForNode(n *sitter.Node) models.SymbolKind {
	switch n.Type() {
	case "function_declaration", "generator_function_declaration", "lexical_declaration":
		return models.KindFunction
	case "class_declaration", "abstract_class_declaration":
		return models.KindClass
	case "interface_declaration":
		return models.KindInterface
	case "type_alias_declaration":
		return models.KindTypeAlias
	case "enum_declaration":
		return models.KindEnum
	case "method_definition":
		return models.KindMethod
	default:
		return models.KindFunction
	}
}

// enclosingClassName walks up the AST looking for a class_declaration or
// abstract_class_declaration ancestor, returning the class name and true.
func enclosingClassName(def *sitter.Node, source []byte) (string, bool) {
	for n := def.Parent(); n != nil; n = n.Parent() {
		switch n.Type() {
		case "class_declaration", "abstract_class_declaration":
			nameNode := n.ChildByFieldName("name")
			if nameNode != nil {
				return nameNode.Content(source), true
			}
		}
	}
	return "", false
}

// collectExportedNames walks the module body and records names exported via
// `export function foo`, `export class Foo`, `export const foo = ...`,
// `export { foo, bar }`, and `export default ...`. The set is consulted to
// flag IsExportedSymbol and build the Exports list.
func collectExportedNames(root *sitter.Node, source []byte, out map[string]struct{}) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		child := root.NamedChild(i)
		if child.Type() != "export_statement" {
			continue
		}
		// Direct declaration form: export function/class/const/...
		if decl := child.ChildByFieldName("declaration"); decl != nil {
			collectDeclNames(decl, source, out)
			continue
		}
		// Named export form: export { a, b as c } from "...".
		// We grab `export_clause` -> `export_specifier` -> name.
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
	case "function_declaration", "generator_function_declaration",
		"class_declaration", "abstract_class_declaration",
		"interface_declaration", "type_alias_declaration",
		"enum_declaration":
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
