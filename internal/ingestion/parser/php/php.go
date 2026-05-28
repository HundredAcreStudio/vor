// Package php is the PHP-language parser. Binds tree-sitter-php and
// extracts classes, interfaces, traits, enums, functions/methods, use
// imports, and calls via the shared generic extractor.
package php

import (
	"context"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/php"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser/common"
)

const langTag models.LanguageTag = "php"

var (
	q     *sitter.Query
	qOnce sync.Once
	qErr  error
)

type Parser struct{}

func init() { parser.Register(langTag, &Parser{}) }

func (p *Parser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	return common.GenericExtract(ctx, fi, source, common.GenericConfig{
		Lang: php.GetLanguage(), QueryName: "php.scm", LangTag: langTag,
		Once: &qOnce, QueryPtr: &q, ErrPtr: &qErr,
		KindFor:    kindFor,
		DefaultVis: models.VisibilityPublic, // PHP members default public
		ContainerTypes: map[string]struct{}{
			"class_declaration": {}, "interface_declaration": {},
			"trait_declaration": {}, "enum_declaration": {},
		},
		BranchTypes: branchTypes,
	})
}

func kindFor(def *sitter.Node, _ []byte) models.SymbolKind {
	switch def.Type() {
	case "class_declaration":
		return models.KindClass
	case "interface_declaration":
		return models.KindInterface
	case "trait_declaration":
		return models.KindTrait
	case "enum_declaration":
		return models.KindEnum
	default: // function_definition, method_declaration
		return models.KindFunction
	}
}

var branchTypes = map[string]struct{}{
	"if_statement": {}, "else_clause": {}, "elseif_clause": {},
	"for_statement": {}, "foreach_statement": {}, "while_statement": {},
	"do_statement": {}, "switch_block": {}, "case_statement": {},
	"catch_clause": {}, "conditional_expression": {},
}
