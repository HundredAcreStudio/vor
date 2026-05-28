// Package ruby is the Ruby-language parser. Binds tree-sitter-ruby and
// extracts modules, classes, methods, require imports, and calls via the
// shared generic extractor.
package ruby

import (
	"context"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/ruby"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser"
	"github.com/repowise-dev/repowise-go/internal/ingestion/parser/common"
)

const langTag models.LanguageTag = "ruby"

var (
	q     *sitter.Query
	qOnce sync.Once
	qErr  error
)

type Parser struct{}

func init() { parser.Register(langTag, &Parser{}) }

func (p *Parser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	return common.GenericExtract(ctx, fi, source, common.GenericConfig{
		Lang: ruby.GetLanguage(), QueryName: "ruby.scm", LangTag: langTag,
		Once: &qOnce, QueryPtr: &q, ErrPtr: &qErr,
		KindFor:    kindFor,
		DefaultVis: models.VisibilityPublic, // Ruby methods are public by default
		ContainerTypes: map[string]struct{}{
			"class": {}, "module": {}, "singleton_class": {},
		},
		BranchTypes:      branchTypes,
		ImportKinds:      map[string]struct{}{"require": {}, "require_relative": {}},
		RelativePrefixes: []string{"./", "../"},
	})
}

func kindFor(def *sitter.Node, _ []byte) models.SymbolKind {
	switch def.Type() {
	case "module":
		return models.KindModule
	case "class":
		return models.KindClass
	default: // method, singleton_method
		return models.KindFunction
	}
}

var branchTypes = map[string]struct{}{
	"if": {}, "elsif": {}, "unless": {}, "while": {}, "until": {},
	"for": {}, "case": {}, "when": {}, "rescue": {}, "ternary": {},
}
