// Package scala is the Scala-language parser. Binds tree-sitter-scala and
// extracts classes, objects, traits, functions/methods, imports, and calls
// via the shared generic extractor.
package scala

import (
	"context"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/scala"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser/common"
)

const langTag models.LanguageTag = "scala"

var (
	q     *sitter.Query
	qOnce sync.Once
	qErr  error
)

type Parser struct{}

func init() { parser.Register(langTag, &Parser{}) }

func (p *Parser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	return common.GenericExtract(ctx, fi, source, common.GenericConfig{
		Lang: scala.GetLanguage(), QueryName: "scala.scm", LangTag: langTag,
		Once: &qOnce, QueryPtr: &q, ErrPtr: &qErr,
		KindFor:    kindFor,
		DefaultVis: models.VisibilityPublic, // Scala defaults public
		ContainerTypes: map[string]struct{}{
			"class_definition": {}, "object_definition": {}, "trait_definition": {},
		},
		BranchTypes: branchTypes,
	})
}

func kindFor(def *sitter.Node, _ []byte) models.SymbolKind {
	switch def.Type() {
	case "class_definition":
		return models.KindClass
	case "object_definition":
		return models.KindClass
	case "trait_definition":
		return models.KindTrait
	default: // function_definition
		return models.KindFunction
	}
}

var branchTypes = map[string]struct{}{
	"if_expression": {}, "match_expression": {}, "case_clause": {},
	"for_expression": {}, "while_expression": {}, "try_expression": {},
	"catch_clause": {},
}
