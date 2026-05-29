// Package rust is the Rust-language parser. Binds tree-sitter-rust and
// applies Rust-specific rules: pub visibility, impl-block parent
// extraction, macro invocations counted as calls.
package rust

import (
	"context"
	"fmt"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/rust"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser/common"
)

const langTag models.LanguageTag = "rust"

var (
	compiledQuery     *sitter.Query
	compiledQueryOnce sync.Once
	compiledQueryErr  error
)

func loadQuery() (*sitter.Query, error) {
	return common.LoadQueryOnce("rust.scm", rust.GetLanguage(),
		&compiledQueryOnce, &compiledQuery, &compiledQueryErr)
}

// Parser is the registered Rust parser.
type Parser struct{}

func init() { parser.Register(langTag, &Parser{}) }

// Parse extracts Rust symbols, imports, and calls. The extraction shell lives
// in common.RunQuery; this wires in Rust's symbol/import/call logic and the
// export rule (pub symbols at top level).
func (p *Parser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	query, err := loadQuery()
	if err != nil {
		return models.ParsedFile{}, err
	}
	return common.RunQuery(ctx, fi, source, common.ParseSpec{
		Lang:         rust.GetLanguage(),
		Query:        query,
		AbsorbSymbol: absorbSymbol,
		AbsorbImport: absorbImport,
		AbsorbCall:   absorbCall,
		Finalize: func(_ *sitter.Node, _ []byte, pf *models.ParsedFile) {
			pf.Exports = common.ExportsPublicTopLevel(pf.Symbols)
		},
	})
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
		mergeVisibility(existing, caps, source)
		return
	}

	complexity, nesting := 1, 0
	if def.Type() == "function_item" {
		complexity = 1 + common.CountBranchNodes(def, rustBranchNodeTypes)
		nesting = common.MaxNestingDepth(def, rustBranchNodeTypes)
	}

	sym := &models.Symbol{
		Name:               name,
		Kind:               kindForNode(def),
		StartLine:          int(def.StartPoint().Row) + 1,
		EndLine:            int(def.EndPoint().Row) + 1,
		Visibility:         models.VisibilityPrivate, // Rust default
		Language:           string(langTag),
		ComplexityEstimate: complexity,
		NestingDepth:       nesting,
		QualifiedName:      name,
	}
	mergeVisibility(sym, caps, source)
	sym.IsExportedSymbol = sym.Visibility == models.VisibilityPublic

	// Functions inside impl blocks get impl-block type as parent.
	if parentName, isMethod := enclosingImplType(def, source); isMethod {
		sym.Kind = models.KindMethod
		sym.ParentName = common.PtrStr(parentName)
		sym.ID = fmt.Sprintf("%s::%s::%s", fi.Path, parentName, name)
		sym.QualifiedName = parentName + "::" + name
	} else {
		sym.ID = fmt.Sprintf("%s::%s", fi.Path, name)
	}

	sym.Signature = strings.TrimSpace(common.SignatureSlice(source, def, caps["symbol.params"]))
	out[startByte] = sym
}

func mergeVisibility(sym *models.Symbol, caps map[string][]*sitter.Node, source []byte) {
	for _, m := range caps["symbol.modifiers"] {
		text := strings.TrimSpace(m.Content(source))
		if strings.HasPrefix(text, "pub") {
			sym.Visibility = models.VisibilityPublic
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
	module := strings.TrimSpace(modules[0].Content(source))
	return &models.Import{
		RawStatement:  raw,
		ModulePath:    module,
		ImportedNames: []string{},
		IsRelative:    strings.HasPrefix(module, "self") || strings.HasPrefix(module, "super") || strings.HasPrefix(module, "crate"),
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

// rustBranchNodeTypes — decision-point nodes in tree-sitter-rust.
// match_arm covers each arm of a match expression. if_let_expression /
// while_let_expression are pattern-matching variants.
var rustBranchNodeTypes = map[string]struct{}{
	"if_expression":        {},
	"if_let_expression":    {},
	"while_expression":     {},
	"while_let_expression": {},
	"for_expression":       {},
	"loop_expression":      {},
	"match_arm":            {},
	"try_expression":       {}, // ?-operator counts as a branch
}

func kindForNode(n *sitter.Node) models.SymbolKind {
	switch n.Type() {
	case "function_item":
		return models.KindFunction
	case "struct_item":
		return models.KindStruct
	case "enum_item":
		return models.KindEnum
	case "trait_item":
		return models.KindTrait
	case "impl_item":
		return models.KindImpl
	case "const_item":
		return models.KindConstant
	case "type_item":
		return models.KindTypeAlias
	case "mod_item":
		return models.KindModule
	case "macro_definition":
		return models.KindMacro
	default:
		return models.KindFunction
	}
}

// enclosingImplType walks the AST for an impl_item ancestor; returns its
// type name and true if found. Used to mark methods with their impl-block
// type as the parent.
func enclosingImplType(def *sitter.Node, source []byte) (string, bool) {
	for n := def.Parent(); n != nil; n = n.Parent() {
		if n.Type() == "impl_item" {
			if typ := n.ChildByFieldName("type"); typ != nil {
				return typ.Content(source), true
			}
		}
	}
	return "", false
}
