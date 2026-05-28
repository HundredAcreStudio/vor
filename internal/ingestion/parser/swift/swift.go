// Package swift is the Swift-language parser. Binds tree-sitter-swift and
// extracts types, protocols, functions/methods, imports, and calls via the
// shared generic extractor. class / struct / enum / actor all parse as
// class_declaration, so the concrete kind is recovered from the leading
// keyword token.
package swift

import (
	"context"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/swift"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser/common"
)

const langTag models.LanguageTag = "swift"

var (
	q     *sitter.Query
	qOnce sync.Once
	qErr  error
)

type Parser struct{}

func init() { parser.Register(langTag, &Parser{}) }

func (p *Parser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	return common.GenericExtract(ctx, fi, source, common.GenericConfig{
		Lang: swift.GetLanguage(), QueryName: "swift.scm", LangTag: langTag,
		Once: &qOnce, QueryPtr: &q, ErrPtr: &qErr,
		KindFor:    kindFor,
		DefaultVis: models.VisibilityPublic, // Swift defaults to internal; treat as visible
		ContainerTypes: map[string]struct{}{
			"class_declaration": {}, "protocol_declaration": {}, "extension_declaration": {},
		},
		BranchTypes: branchTypes,
	})
}

func kindFor(def *sitter.Node, _ []byte) models.SymbolKind {
	switch def.Type() {
	case "protocol_declaration":
		return models.KindInterface
	case "function_declaration":
		return models.KindFunction
	case "class_declaration":
		// The leading keyword token distinguishes class/struct/enum/actor.
		for i := 0; i < int(def.ChildCount()); i++ {
			switch def.Child(i).Type() {
			case "struct":
				return models.KindStruct
			case "enum":
				return models.KindEnum
			case "actor", "class":
				return models.KindClass
			}
		}
		return models.KindClass
	default:
		return models.KindFunction
	}
}

var branchTypes = map[string]struct{}{
	"if_statement": {}, "guard_statement": {}, "for_statement": {},
	"while_statement": {}, "switch_statement": {}, "switch_entry": {},
	"catch_block": {}, "ternary_expression": {},
}
