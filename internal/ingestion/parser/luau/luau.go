// Package luau is the Lua / Luau parser. Luau is a Lua superset, so it
// binds tree-sitter-lua and extracts function definitions, require imports,
// and calls via the shared generic extractor. Registered under the "luau"
// language tag (which also covers plain .lua files).
package luau

import (
	"context"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/lua"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser/common"
)

const langTag models.LanguageTag = "luau"

var (
	q     *sitter.Query
	qOnce sync.Once
	qErr  error
)

type Parser struct{}

func init() { parser.Register(langTag, &Parser{}) }

func (p *Parser) Parse(ctx context.Context, fi models.FileInfo, source []byte) (models.ParsedFile, error) {
	return common.GenericExtract(ctx, fi, source, common.GenericConfig{
		Lang: lua.GetLanguage(), QueryName: "luau.scm", LangTag: langTag,
		Once: &qOnce, QueryPtr: &q, ErrPtr: &qErr,
		KindFor:     func(*sitter.Node, []byte) models.SymbolKind { return models.KindFunction },
		DefaultVis:  models.VisibilityPublic, // Lua has no access keywords
		BranchTypes: branchTypes,
		ImportKinds: map[string]struct{}{"require": {}},
	})
}

var branchTypes = map[string]struct{}{
	"if_statement": {}, "elseif_statement": {}, "else_statement": {},
	"for_statement": {}, "for_in_statement": {}, "while_statement": {},
	"repeat_statement": {},
}
