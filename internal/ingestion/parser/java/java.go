// Package java is the Java-language parser. Binds tree-sitter-java and
// handles classes, interfaces, enums, records (Java 16+), methods,
// constructors, plus modifier-based visibility extraction.
package java

import (
	"context"
	"fmt"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/java"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser/common"
)

const langTag models.LanguageTag = "java"

var (
	compiledQuery     *sitter.Query
	compiledQueryOnce sync.Once
	compiledQueryErr  error
)

func loadQuery() (*sitter.Query, error) {
	return common.LoadQueryOnce("java.scm", java.GetLanguage(),
		&compiledQueryOnce, &compiledQuery, &compiledQueryErr)
}

// Parser is the registered Java parser.
type Parser struct{}

func init() { parser.Register(langTag, &Parser{}) }

// Parse extracts Java symbols, imports, and calls.
func (p *Parser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	if err := ctx.Err(); err != nil {
		return models.ParsedFile{}, err
	}

	tsp := sitter.NewParser()
	tsp.SetLanguage(java.GetLanguage())
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

	complexity, nesting := 1, 0
	switch def.Type() {
	case "method_declaration", "constructor_declaration":
		complexity = 1 + common.CountBranchNodes(def, javaBranchNodeTypes)
		nesting = common.MaxNestingDepth(def, javaBranchNodeTypes)
	}

	sym := &models.Symbol{
		Name:               name,
		Kind:               kindForNode(def),
		StartLine:          int(def.StartPoint().Row) + 1,
		EndLine:            int(def.EndPoint().Row) + 1,
		Visibility:         models.VisibilityInternal, // Java "package-private" default
		Language:           string(langTag),
		ComplexityEstimate: complexity,
		NestingDepth:       nesting,
		QualifiedName:      name,
	}
	mergeModifiers(sym, caps, source)
	sym.IsExportedSymbol = sym.Visibility == models.VisibilityPublic

	if parentName, isMember := enclosingTypeName(def, source); isMember {
		switch def.Type() {
		case "method_declaration", "constructor_declaration":
			sym.Kind = models.KindMethod
		}
		sym.ParentName = common.PtrStr(parentName)
		sym.ID = fmt.Sprintf("%s::%s::%s", fi.Path, parentName, name)
		sym.QualifiedName = parentName + "." + name
	} else {
		sym.ID = fmt.Sprintf("%s::%s", fi.Path, name)
	}

	sym.Signature = strings.TrimSpace(common.SignatureSlice(source, def, caps["symbol.params"]))
	out[startByte] = sym
}

func mergeModifiers(sym *models.Symbol, caps map[string][]*sitter.Node, source []byte) {
	for _, m := range caps["symbol.modifiers"] {
		text := m.Content(source)
		switch {
		case strings.Contains(text, "public"):
			sym.Visibility = models.VisibilityPublic
		case strings.Contains(text, "private"):
			sym.Visibility = models.VisibilityPrivate
		case strings.Contains(text, "protected"):
			sym.Visibility = models.VisibilityProtected
		}
		// Annotation-style modifiers (@Override, @SuppressWarnings) become
		// decorators.
		for _, line := range strings.Fields(text) {
			if strings.HasPrefix(line, "@") {
				sym.Decorators = append(sym.Decorators, line)
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
	return &models.Import{
		RawStatement:  stmt.Content(source),
		ModulePath:    strings.TrimSpace(modules[0].Content(source)),
		ImportedNames: []string{},
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

var javaBranchNodeTypes = map[string]struct{}{
	"if_statement":           {},
	"for_statement":          {},
	"enhanced_for_statement": {},
	"while_statement":        {},
	"do_statement":           {},
	"switch_label":           {},
	"catch_clause":           {},
	"ternary_expression":     {},
}

func kindForNode(n *sitter.Node) models.SymbolKind {
	switch n.Type() {
	case "class_declaration":
		return models.KindClass
	case "interface_declaration":
		return models.KindInterface
	case "enum_declaration":
		return models.KindEnum
	case "record_declaration":
		return models.KindStruct
	case "method_declaration", "constructor_declaration":
		return models.KindFunction
	default:
		return models.KindFunction
	}
}

func enclosingTypeName(def *sitter.Node, source []byte) (string, bool) {
	for n := def.Parent(); n != nil; n = n.Parent() {
		switch n.Type() {
		case "class_declaration", "interface_declaration",
			"enum_declaration", "record_declaration":
			if nm := n.ChildByFieldName("name"); nm != nil {
				return nm.Content(source), true
			}
		}
	}
	return "", false
}
