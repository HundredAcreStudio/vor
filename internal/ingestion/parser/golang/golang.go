// Package golang is the Go-language parser. It wraps tree-sitter-go via
// smacker/go-tree-sitter (cgo) and walks the AST guided by the Go .scm
// query file embedded in internal/ingestion/queries.
package golang

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser/common"
)

const langTag models.LanguageTag = "go"

var (
	compiledQuery     *sitter.Query
	compiledQueryOnce sync.Once
	compiledQueryErr  error
)

func loadQuery() (*sitter.Query, error) {
	return common.LoadQueryOnce("go.scm", golang.GetLanguage(), &compiledQueryOnce, &compiledQuery, &compiledQueryErr)
}

// Parser is the registered Go parser.
type Parser struct{}

func init() { parser.Register(langTag, &Parser{}) }

// Parse extracts Go symbols, imports, and calls from source.
func (p *Parser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	if err := ctx.Err(); err != nil {
		return models.ParsedFile{}, err
	}

	tsp := sitter.NewParser()
	tsp.SetLanguage(golang.GetLanguage())
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

	exports := make([]string, 0)
	for _, s := range symbols {
		if s.IsExportedSymbol {
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

// ---- Go-specific symbol/import/call construction ---------------------------

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
	if def.Type() == "function_declaration" || def.Type() == "method_declaration" {
		complexity = 1 + common.CountBranchNodes(def, goBranchNodeTypes)
		nesting = common.MaxNestingDepth(def, goBranchNodeTypes)
	}

	sym := &models.Symbol{
		Name:               name,
		Kind:               kindForNode(def),
		StartLine:          int(def.StartPoint().Row) + 1,
		EndLine:            int(def.EndPoint().Row) + 1,
		Visibility:         goVisibility(name),
		Language:           string(langTag),
		ComplexityEstimate: complexity,
		NestingDepth:       nesting,
		IsExportedSymbol:   isExported(name),
		QualifiedName:      name,
	}

	if recv := caps["symbol.receiver"]; len(recv) > 0 {
		parent := goReceiverType(recv[0].Content(source))
		if parent != "" {
			sym.ParentName = common.PtrStr(parent)
		}
		sym.Kind = models.KindMethod
		sym.ID = fmt.Sprintf("%s::%s::%s", fi.Path, parent, name)
	} else {
		sym.ID = fmt.Sprintf("%s::%s", fi.Path, name)
	}

	sym.Signature = strings.TrimSpace(common.SignatureSlice(source, def, caps["symbol.params"]))
	out[startByte] = sym
}

// goBranchNodeTypes are the tree-sitter-go node types that add one to
// cyclomatic complexity when seen inside a function body. We count the
// case/communication-case nodes (one per branch) rather than counting
// switch_statement itself; that's pure McCabe — a switch with N cases
// adds N decision points, not 1.
//
// Short-circuit boolean operators (&& / ||) are not counted: tree-sitter
// represents them under the broad `binary_expression` node which also
// covers arithmetic, comparison, etc., so a tighter check would need to
// inspect the operator field. Trade-off is documented; if it matters we
// can add an "extended McCabe" mode later.
var goBranchNodeTypes = map[string]struct{}{
	"if_statement":       {},
	"for_statement":      {},
	"select_statement":   {},
	"expression_case":    {},
	"type_case":          {},
	"communication_case": {},
	// default_case is intentionally absent — it is the "else" path that
	// already exists implicitly; counting it would double-count one path.
}

func absorbImport(caps map[string][]*sitter.Node, source []byte) *models.Import {
	stmt := caps["import.statement"][0]
	modules := caps["import.module"]
	if len(modules) == 0 {
		return nil
	}
	return &models.Import{
		RawStatement:  stmt.Content(source),
		ModulePath:    strings.Trim(modules[0].Content(source), `"`),
		ImportedNames: []string{},
		IsRelative:    false,
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

// ---- Go-specific naming rules ---------------------------------------------

func kindForNode(n *sitter.Node) models.SymbolKind {
	switch n.Type() {
	case "function_declaration":
		return models.KindFunction
	case "method_declaration":
		return models.KindMethod
	case "type_spec":
		return models.KindClass
	case "const_spec":
		return models.KindConstant
	case "var_spec":
		return models.KindVariable
	default:
		return models.KindFunction
	}
}

func goVisibility(name string) models.Visibility {
	if isExported(name) {
		return models.VisibilityPublic
	}
	return models.VisibilityPrivate
}

func isExported(name string) bool {
	if name == "" {
		return false
	}
	r := []rune(name)[0]
	return unicode.IsUpper(r)
}

// goReceiverType pulls the type name out of a `(r *Receiver)` parameter list.
//
//	"(r *MyType)"      -> "MyType"
//	"(MyType)"         -> "MyType"
//	"(*MyType)"        -> "MyType"
//	"(s *Server[K,V])" -> "Server"
func goReceiverType(recvText string) string {
	t := strings.TrimSpace(recvText)
	t = strings.TrimPrefix(t, "(")
	t = strings.TrimSuffix(t, ")")
	t = strings.TrimSpace(t)

	// Drop the type-parameter section first so any inner spaces don't fool
	// the receiver-name split.
	if i := strings.IndexByte(t, '['); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	if i := strings.LastIndex(t, " "); i >= 0 {
		t = t[i+1:]
	}
	t = strings.TrimPrefix(t, "*")
	return t
}
