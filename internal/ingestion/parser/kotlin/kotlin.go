// Package kotlin is the Kotlin-language parser. Binds tree-sitter-kotlin
// and extracts classes, objects, functions/methods, imports, and calls via
// the shared generic extractor. interface parses as class_declaration with
// an "interface" keyword, recovered in Go.
package kotlin

import (
	"context"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/kotlin"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser/common"
)

const langTag models.LanguageTag = "kotlin"

var (
	q     *sitter.Query
	qOnce sync.Once
	qErr  error
)

type Parser struct{}

func init() { parser.Register(langTag, &Parser{}) }

func (p *Parser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	return common.GenericExtract(ctx, fi, source, common.GenericConfig{
		Lang: kotlin.GetLanguage(), QueryName: "kotlin.scm", LangTag: langTag,
		Once: &qOnce, QueryPtr: &q, ErrPtr: &qErr,
		KindFor:    kindFor,
		DefaultVis: models.VisibilityPublic, // Kotlin declarations default public
		ContainerTypes: map[string]struct{}{
			"class_declaration": {}, "object_declaration": {},
		},
		BranchTypes: branchTypes,
	})
}

func kindFor(def *sitter.Node, _ []byte) models.SymbolKind {
	switch def.Type() {
	case "object_declaration":
		return models.KindClass
	case "function_declaration":
		return models.KindFunction
	case "class_declaration":
		for i := 0; i < int(def.ChildCount()); i++ {
			if def.Child(i).Type() == "interface" {
				return models.KindInterface
			}
		}
		return models.KindClass
	default:
		return models.KindFunction
	}
}

var branchTypes = map[string]struct{}{
	"if_expression": {}, "when_expression": {}, "when_entry": {},
	"for_statement": {}, "while_statement": {}, "do_while_statement": {},
	"catch_block": {}, "elvis_expression": {},
}
