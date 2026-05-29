// Package cpp is the C / C++ parser. Both languages share the tree-sitter-cpp
// grammar (the C grammar isn't separately bundled in smacker/go-tree-sitter),
// so a single backing parser handles both. Two parsers are registered against
// the language registry so kind / visibility decisions can still differ
// per language if needed; currently they share the same logic.
package cpp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/cpp"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser/common"
)

var (
	cppQuery     *sitter.Query
	cppQueryOnce sync.Once
	cppQueryErr  error

	cQuery     *sitter.Query
	cQueryOnce sync.Once
	cQueryErr  error
)

func loadCppQuery() (*sitter.Query, error) {
	return common.LoadQueryOnce("cpp.scm", cpp.GetLanguage(),
		&cppQueryOnce, &cppQuery, &cppQueryErr)
}

func loadCQuery() (*sitter.Query, error) {
	return common.LoadQueryOnce("c.scm", cpp.GetLanguage(),
		&cQueryOnce, &cQuery, &cQueryErr)
}

// CppParser handles .cpp/.cc/.cxx/.hpp/.hxx files.
type CppParser struct{}

// CParser handles .c/.h files using a narrower query.
type CParser struct{}

func init() {
	parser.Register("cpp", &CppParser{})
	parser.Register("c", &CParser{})
}

// Parse — CppParser. Delegates to the shared implementation with the
// C++ query.
func (p *CppParser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	return parseInternal(ctx, fi, source, "cpp", loadCppQuery)
}

// Parse — CParser. Same shared implementation, narrower query.
func (p *CParser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	return parseInternal(ctx, fi, source, "c", loadCQuery)
}

// parseInternal is the shared C/C++ implementation. Both variants use the C++
// grammar (the C parser just runs a narrower query); the extraction shell
// lives in common.RunQuery.
func parseInternal(ctx context.Context, fi models.FileInfo, source []byte, langTag models.LanguageTag, loadQuery func() (*sitter.Query, error)) (models.ParsedFile, error) {
	query, err := loadQuery()
	if err != nil {
		return models.ParsedFile{}, err
	}
	return common.RunQuery(ctx, fi, source, common.ParseSpec{
		Lang:  cpp.GetLanguage(),
		Query: query,
		AbsorbSymbol: func(out map[uint32]*models.Symbol, caps map[string][]*sitter.Node, fi models.FileInfo, source []byte) {
			absorbSymbol(out, caps, fi, langTag, source)
		},
		AbsorbImport: absorbImport,
		AbsorbCall:   absorbCall,
		Finalize: func(_ *sitter.Node, _ []byte, pf *models.ParsedFile) {
			// C/C++ has no module-level visibility; surface every symbol name.
			exports := make([]string, 0, len(pf.Symbols))
			for _, s := range pf.Symbols {
				exports = append(exports, s.Name)
			}
			pf.Exports = exports
		},
	})
}

func absorbSymbol(out map[uint32]*models.Symbol, caps map[string][]*sitter.Node, fi models.FileInfo, langTag models.LanguageTag, source []byte) {
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
	if def.Type() == "function_definition" {
		complexity = 1 + common.CountBranchNodes(def, cppBranchNodeTypes)
		nesting = common.MaxNestingDepth(def, cppBranchNodeTypes)
	}

	sym := &models.Symbol{
		Name:               name,
		Kind:               kindForNode(def),
		StartLine:          int(def.StartPoint().Row) + 1,
		EndLine:            int(def.EndPoint().Row) + 1,
		Visibility:         models.VisibilityPublic, // C/C++ has no symbol-level "public" the way the schema means it
		Language:           string(langTag),
		ComplexityEstimate: complexity,
		NestingDepth:       nesting,
		QualifiedName:      name,
	}

	// Methods inside a class body: the symbol.name capture is a
	// field_identifier and the enclosing class_specifier supplies the
	// parent name.
	if nameNodes[0].Type() == "field_identifier" {
		if parentName, isMember := enclosingClassName(def, source); isMember {
			sym.Kind = models.KindMethod
			sym.ParentName = common.PtrStr(parentName)
			sym.ID = fmt.Sprintf("%s::%s::%s", fi.Path, parentName, name)
			sym.QualifiedName = parentName + "::" + name
		}
	}

	// Out-of-line qualified definition: ReturnType ClassName::method()
	// — the captured @symbol.name lives inside a qualified_identifier.
	if parent := nameNodes[0].Parent(); parent != nil && parent.Type() == "qualified_identifier" {
		if scope := parent.ChildByFieldName("scope"); scope != nil {
			parentName := scope.Content(source)
			sym.Kind = models.KindMethod
			sym.ParentName = common.PtrStr(parentName)
			sym.ID = fmt.Sprintf("%s::%s::%s", fi.Path, parentName, name)
			sym.QualifiedName = parentName + "::" + name
		}
	}

	if sym.ID == "" {
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
	module := modules[0].Content(source)
	// Strip <> or "" delimiters.
	module = strings.TrimPrefix(module, "<")
	module = strings.TrimSuffix(module, ">")
	module = strings.Trim(module, `"`)
	return &models.Import{
		RawStatement:  stmt.Content(source),
		ModulePath:    module,
		ImportedNames: []string{},
		IsRelative:    !strings.HasPrefix(stmt.Content(source), "#include <"),
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

var cppBranchNodeTypes = map[string]struct{}{
	"if_statement":           {},
	"for_statement":          {},
	"for_range_loop":         {},
	"while_statement":        {},
	"do_statement":           {},
	"case_statement":         {},
	"catch_clause":           {},
	"conditional_expression": {},
}

func kindForNode(n *sitter.Node) models.SymbolKind {
	switch n.Type() {
	case "function_definition":
		return models.KindFunction
	case "class_specifier":
		return models.KindClass
	case "struct_specifier":
		return models.KindStruct
	case "enum_specifier":
		return models.KindEnum
	case "namespace_definition":
		return models.KindModule
	default:
		return models.KindFunction
	}
}

// enclosingClassName walks ancestors for class_specifier / struct_specifier.
func enclosingClassName(def *sitter.Node, source []byte) (string, bool) {
	for n := def.Parent(); n != nil; n = n.Parent() {
		switch n.Type() {
		case "class_specifier", "struct_specifier":
			if nm := n.ChildByFieldName("name"); nm != nil {
				return nm.Content(source), true
			}
		}
	}
	return "", false
}
