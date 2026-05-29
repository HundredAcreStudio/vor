package common

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/ruby"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

// ---- test helpers ----------------------------------------------------------

// goBranch is a minimal decision-point set for complexity/nesting assertions.
var goBranch = map[string]struct{}{"if_statement": {}, "for_statement": {}}

// standardQuery captures the universal contract RunQuery dispatches on:
// symbol.def/.name/.params, import.statement/.module, call.site/.target/.arguments.
const standardQuery = `
(function_declaration
  name: (identifier) @symbol.name
  parameters: (parameter_list) @symbol.params) @symbol.def
(import_spec
  path: (interpreted_string_literal) @import.module) @import.statement
(call_expression
  function: (_) @call.target
  arguments: (argument_list) @call.arguments) @call.site
`

func mustGoQuery(t *testing.T, pattern string) *sitter.Query {
	t.Helper()
	q, err := sitter.NewQuery([]byte(pattern), golang.GetLanguage())
	if err != nil {
		t.Fatalf("compile query: %v", err)
	}
	return q
}

func parseGo(t *testing.T, src []byte) *sitter.Tree {
	t.Helper()
	p := sitter.NewParser()
	p.SetLanguage(golang.GetLanguage())
	tree, err := p.ParseCtx(context.Background(), nil, src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return tree
}

// firstDescendant returns the first node of the given type in n's subtree
// (depth-first), or nil. Lets tests grab a node deep inside the AST.
func firstDescendant(n *sitter.Node, typ string) *sitter.Node {
	stack := []*sitter.Node{n}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.Type() == typ {
			return cur
		}
		// Push children in reverse so the leftmost is popped first → the node
		// returned is the first match in document (pre-order) order.
		for i := int(cur.NamedChildCount()) - 1; i >= 0; i-- {
			stack = append(stack, cur.NamedChild(i))
		}
	}
	return nil
}

// goSymbolAbsorber builds a Go-flavoured symbol absorber: exported = capitalised
// first rune, deduped by start byte. It also exercises CountBranchNodes,
// MaxNestingDepth, and SignatureSlice inside the RunQuery flow.
func goSymbolAbsorber(out map[uint32]*models.Symbol, caps map[string][]*sitter.Node, fi models.FileInfo, source []byte) {
	def := caps["symbol.def"][0]
	name := caps["symbol.name"][0].Content(source)
	start := def.StartByte()
	if _, exists := out[start]; exists {
		return // dedupe repeated matches for the same definition
	}
	vis := models.VisibilityPrivate
	exported := false
	if r := []rune(name); len(r) > 0 && unicode.IsUpper(r[0]) {
		vis = models.VisibilityPublic
		exported = true
	}
	out[start] = &models.Symbol{
		ID:                 fi.Path + "::" + name,
		Name:               name,
		Kind:               models.KindFunction,
		StartLine:          int(def.StartPoint().Row) + 1,
		EndLine:            int(def.EndPoint().Row) + 1,
		Visibility:         vis,
		IsExportedSymbol:   exported,
		ComplexityEstimate: 1 + CountBranchNodes(def, goBranch),
		NestingDepth:       MaxNestingDepth(def, goBranch),
		Signature:          strings.TrimSpace(SignatureSlice(source, def, caps["symbol.params"])),
	}
}

func goImportAbsorber(caps map[string][]*sitter.Node, source []byte) *models.Import {
	mod := strings.Trim(caps["import.module"][0].Content(source), `"`)
	return &models.Import{ModulePath: mod, RawStatement: caps["import.statement"][0].Content(source)}
}

func goCallAbsorber(caps map[string][]*sitter.Node, source []byte) *models.CallSite {
	target := caps["call.target"][0]
	n := int(caps["call.arguments"][0].NamedChildCount())
	return &models.CallSite{
		TargetName:    target.Content(source),
		Line:          int(target.StartPoint().Row) + 1,
		ArgumentCount: &n,
	}
}

func symByName(syms []models.Symbol, name string) *models.Symbol {
	for i := range syms {
		if syms[i].Name == name {
			return &syms[i]
		}
	}
	return nil
}

// greetSrc has known line numbers used across RunQuery tests:
//
//	5  func Greet(name string) string {
//	6      if name == "" {
//	7          return "hi"
//	8      }
//	9      return fmt.Sprintf("hi %s", name)
//	10 }
//	12 func unexported() {}
var greetSrc = []byte(`package demo

import "fmt"

func Greet(name string) string {
	if name == "" {
		return "hi"
	}
	return fmt.Sprintf("hi %s", name)
}

func unexported() {}
`)

// ---- RunQuery (driver.go) --------------------------------------------------

func TestRunQuery_ExtractsSymbolsImportsCalls(t *testing.T) {
	fi := models.FileInfo{Path: "demo.go", Language: "go"}
	pf, err := RunQuery(context.Background(), fi, greetSrc, ParseSpec{
		Lang:         golang.GetLanguage(),
		Query:        mustGoQuery(t, standardQuery),
		AbsorbSymbol: goSymbolAbsorber,
		AbsorbImport: goImportAbsorber,
		AbsorbCall:   goCallAbsorber,
		Finalize: func(_ *sitter.Node, _ []byte, pf *models.ParsedFile) {
			pf.Exports = ExportsByFlag(pf.Symbols)
		},
	})
	if err != nil {
		t.Fatalf("RunQuery: %v", err)
	}

	// Two symbols, sorted by start line: Greet then unexported.
	if len(pf.Symbols) != 2 {
		t.Fatalf("got %d symbols, want 2: %+v", len(pf.Symbols), pf.Symbols)
	}
	if pf.Symbols[0].Name != "Greet" || pf.Symbols[1].Name != "unexported" {
		t.Errorf("symbols not sorted by start line: %q, %q", pf.Symbols[0].Name, pf.Symbols[1].Name)
	}

	greet := symByName(pf.Symbols, "Greet")
	if greet.StartLine != 5 || greet.EndLine != 10 {
		t.Errorf("Greet lines = %d-%d, want 5-10", greet.StartLine, greet.EndLine)
	}
	if greet.Visibility != models.VisibilityPublic || !greet.IsExportedSymbol {
		t.Errorf("Greet visibility/export = %v/%v, want public/true", greet.Visibility, greet.IsExportedSymbol)
	}
	if greet.ComplexityEstimate != 2 { // 1 + one if_statement
		t.Errorf("Greet complexity = %d, want 2", greet.ComplexityEstimate)
	}
	if greet.NestingDepth != 1 {
		t.Errorf("Greet nesting = %d, want 1", greet.NestingDepth)
	}
	if greet.Signature != "func Greet(name string)" {
		t.Errorf("Greet signature = %q", greet.Signature)
	}

	if un := symByName(pf.Symbols, "unexported"); un.Visibility != models.VisibilityPrivate || un.IsExportedSymbol {
		t.Errorf("unexported visibility/export = %v/%v, want private/false", un.Visibility, un.IsExportedSymbol)
	}

	// Import: fmt.
	if len(pf.Imports) != 1 || pf.Imports[0].ModulePath != "fmt" {
		t.Fatalf("imports = %+v, want one with ModulePath fmt", pf.Imports)
	}

	// Call: fmt.Sprintf on line 9, resolved to Greet as the enclosing symbol.
	if len(pf.Calls) != 1 {
		t.Fatalf("got %d calls, want 1: %+v", len(pf.Calls), pf.Calls)
	}
	call := pf.Calls[0]
	if call.Line != 9 {
		t.Errorf("call line = %d, want 9", call.Line)
	}
	if call.ArgumentCount == nil || *call.ArgumentCount != 2 {
		t.Errorf("call args = %v, want 2", call.ArgumentCount)
	}
	if call.CallerSymbolID == nil || *call.CallerSymbolID != "demo.go::Greet" {
		t.Errorf("call caller = %v, want demo.go::Greet", call.CallerSymbolID)
	}

	// Finalize ran: ExportsByFlag picked only the exported symbol.
	if len(pf.Exports) != 1 || pf.Exports[0] != "Greet" {
		t.Errorf("exports = %v, want [Greet]", pf.Exports)
	}
}

func TestRunQuery_DedupesSymbolsByStartByte(t *testing.T) {
	// Two identical patterns make every function match twice; the absorber's
	// start-byte keying (against the shared out map RunQuery threads through)
	// must collapse them to one symbol each.
	dupQuery := `
(function_declaration name: (identifier) @symbol.name) @symbol.def
(function_declaration name: (identifier) @symbol.name) @symbol.def
`
	fi := models.FileInfo{Path: "demo.go"}
	pf, err := RunQuery(context.Background(), fi, greetSrc, ParseSpec{
		Lang:         golang.GetLanguage(),
		Query:        mustGoQuery(t, dupQuery),
		AbsorbSymbol: goSymbolAbsorber,
	})
	if err != nil {
		t.Fatalf("RunQuery: %v", err)
	}
	if len(pf.Symbols) != 2 {
		t.Errorf("got %d symbols, want 2 (deduped): %+v", len(pf.Symbols), pf.Symbols)
	}
}

func TestRunQuery_NilOptionalAbsorbers(t *testing.T) {
	// AbsorbImport / AbsorbCall are nil: matching imports and calls must be
	// skipped without panicking. Symbols still extracted.
	fi := models.FileInfo{Path: "demo.go"}
	pf, err := RunQuery(context.Background(), fi, greetSrc, ParseSpec{
		Lang:         golang.GetLanguage(),
		Query:        mustGoQuery(t, standardQuery),
		AbsorbSymbol: goSymbolAbsorber,
	})
	if err != nil {
		t.Fatalf("RunQuery: %v", err)
	}
	if len(pf.Imports) != 0 {
		t.Errorf("imports = %+v, want none", pf.Imports)
	}
	if len(pf.Calls) != 0 {
		t.Errorf("calls = %+v, want none", pf.Calls)
	}
	if len(pf.Symbols) != 2 {
		t.Errorf("symbols = %d, want 2", len(pf.Symbols))
	}
	if pf.Exports != nil {
		t.Errorf("exports = %v, want nil (no Finalize)", pf.Exports)
	}
}

func TestRunQuery_AbsorberReturningNilSkips(t *testing.T) {
	// An AbsorbCall that returns nil for every match drops all calls.
	fi := models.FileInfo{Path: "demo.go"}
	pf, err := RunQuery(context.Background(), fi, greetSrc, ParseSpec{
		Lang:         golang.GetLanguage(),
		Query:        mustGoQuery(t, standardQuery),
		AbsorbSymbol: goSymbolAbsorber,
		AbsorbCall:   func(map[string][]*sitter.Node, []byte) *models.CallSite { return nil },
	})
	if err != nil {
		t.Fatalf("RunQuery: %v", err)
	}
	if len(pf.Calls) != 0 {
		t.Errorf("calls = %+v, want none", pf.Calls)
	}
}

func TestRunQuery_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunQuery(ctx, models.FileInfo{Path: "demo.go"}, greetSrc, ParseSpec{
		Lang:         golang.GetLanguage(),
		Query:        mustGoQuery(t, standardQuery),
		AbsorbSymbol: goSymbolAbsorber,
	})
	if err == nil {
		t.Fatal("RunQuery with cancelled context returned nil error")
	}
}

// ---- ExportsByFlag / ExportsPublicTopLevel (driver.go) ---------------------

func TestExportsByFlag(t *testing.T) {
	syms := []models.Symbol{
		{Name: "Exported", IsExportedSymbol: true},
		{Name: "hidden", IsExportedSymbol: false},
		{Name: "AlsoExported", IsExportedSymbol: true},
	}
	got := ExportsByFlag(syms)
	want := []string{"Exported", "AlsoExported"}
	if !slices.Equal(got, want) {
		t.Errorf("ExportsByFlag = %v, want %v", got, want)
	}
	// Always non-nil, even with no exports (callers assign to a slice field).
	if got := ExportsByFlag(nil); got == nil {
		t.Error("ExportsByFlag(nil) = nil, want empty non-nil slice")
	}
}

func TestExportsPublicTopLevel(t *testing.T) {
	parent := "Container"
	syms := []models.Symbol{
		{Name: "PublicTop", Visibility: models.VisibilityPublic},
		{Name: "privateTop", Visibility: models.VisibilityPrivate},
		{Name: "nestedPublic", Visibility: models.VisibilityPublic, ParentName: &parent}, // has parent → excluded
		{Name: "AnotherTop", Visibility: models.VisibilityPublic},
	}
	got := ExportsPublicTopLevel(syms)
	want := []string{"PublicTop", "AnotherTop"}
	if !slices.Equal(got, want) {
		t.Errorf("ExportsPublicTopLevel = %v, want %v", got, want)
	}
}

// ---- BucketCaptures / EnclosingSymbolID / SortByStartLine (common.go) ------

func TestBucketCaptures(t *testing.T) {
	tree := parseGo(t, greetSrc)
	defer tree.Close()
	q := mustGoQuery(t, standardQuery)
	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(q, tree.RootNode())

	var symbolCaps map[string][]*sitter.Node
	for {
		m, ok := cursor.NextMatch()
		if !ok {
			break
		}
		caps := BucketCaptures(m, q)
		if len(caps["symbol.def"]) > 0 {
			symbolCaps = caps
			break
		}
	}
	if symbolCaps == nil {
		t.Fatal("no symbol match found")
	}
	for _, name := range []string{"symbol.def", "symbol.name", "symbol.params"} {
		if len(symbolCaps[name]) != 1 {
			t.Errorf("bucket %q = %d nodes, want 1", name, len(symbolCaps[name]))
		}
	}
	if len(symbolCaps["call.target"]) != 0 {
		t.Errorf("symbol match unexpectedly has call.target captures")
	}
}

func TestEnclosingSymbolID(t *testing.T) {
	syms := []models.Symbol{
		{ID: "outer", StartLine: 1, EndLine: 20},
		{ID: "inner", StartLine: 5, EndLine: 10}, // smaller range inside outer
	}
	tests := []struct {
		line int
		want *string
	}{
		{line: 7, want: ptr("inner")},  // smallest enclosing wins
		{line: 3, want: ptr("outer")},  // only outer contains it
		{line: 20, want: ptr("outer")}, // inclusive end
		{line: 25, want: nil},          // out of range
	}
	for _, tc := range tests {
		got := EnclosingSymbolID(syms, tc.line)
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("line %d: got %q, want nil", tc.line, *got)
		case tc.want != nil && got == nil:
			t.Errorf("line %d: got nil, want %q", tc.line, *tc.want)
		case tc.want != nil && got != nil && *got != *tc.want:
			t.Errorf("line %d: got %q, want %q", tc.line, *got, *tc.want)
		}
	}
}

func TestSortByStartLine(t *testing.T) {
	syms := []models.Symbol{
		{Name: "c", StartLine: 30},
		{Name: "a", StartLine: 10},
		{Name: "b", StartLine: 20},
	}
	SortByStartLine(syms)
	if syms[0].Name != "a" || syms[1].Name != "b" || syms[2].Name != "c" {
		t.Errorf("sort order = %q, %q, %q", syms[0].Name, syms[1].Name, syms[2].Name)
	}
}

// ---- CountBranchNodes / MaxNestingDepth / EnclosingContainerName -----------

func TestCountBranchNodes(t *testing.T) {
	src := []byte(`package p
func f() {
	if a {
	}
	for i := 0; i < 3; i++ {
		if b {
		}
	}
}
`)
	tree := parseGo(t, src)
	defer tree.Close()
	fn := firstDescendant(tree.RootNode(), "function_declaration")
	if fn == nil {
		t.Fatal("no function_declaration")
	}
	// Two if_statements + one for_statement = 3.
	if got := CountBranchNodes(fn, goBranch); got != 3 {
		t.Errorf("CountBranchNodes = %d, want 3", got)
	}
	if got := CountBranchNodes(nil, goBranch); got != 0 {
		t.Errorf("CountBranchNodes(nil) = %d, want 0", got)
	}
}

func TestMaxNestingDepth(t *testing.T) {
	src := []byte(`package p
func f() {
	if a {
		for i := 0; i < 3; i++ {
			if b {
			}
		}
	}
}
`)
	tree := parseGo(t, src)
	defer tree.Close()
	fn := firstDescendant(tree.RootNode(), "function_declaration")
	// if → for → if = depth 3.
	if got := MaxNestingDepth(fn, goBranch); got != 3 {
		t.Errorf("MaxNestingDepth = %d, want 3", got)
	}
	if got := MaxNestingDepth(nil, goBranch); got != 0 {
		t.Errorf("MaxNestingDepth(nil) = %d, want 0", got)
	}
}

func TestEnclosingContainerName(t *testing.T) {
	tree := parseGo(t, greetSrc)
	defer tree.Close()
	// Any identifier inside Greet's body; its enclosing function_declaration
	// is named via the "name" field.
	fn := firstDescendant(tree.RootNode(), "function_declaration")
	inner := firstDescendant(fn.ChildByFieldName("body"), "identifier")
	if inner == nil {
		t.Fatal("no identifier inside function body")
	}

	containers := map[string]struct{}{"function_declaration": {}}
	if name, ok := EnclosingContainerName(inner, containers, greetSrc); !ok || name != "Greet" {
		t.Errorf("EnclosingContainerName = (%q, %v), want (Greet, true)", name, ok)
	}

	// nil container set → never matches.
	if _, ok := EnclosingContainerName(inner, nil, greetSrc); ok {
		t.Error("EnclosingContainerName with nil containers returned ok")
	}

	// container type that never appears → no match.
	none := map[string]struct{}{"nonexistent_type": {}}
	if _, ok := EnclosingContainerName(inner, none, greetSrc); ok {
		t.Error("EnclosingContainerName matched a type that does not appear")
	}
}

// ---- SignatureSlice / FirstLine (common.go) --------------------------------

func TestFirstLine(t *testing.T) {
	if got := string(FirstLine([]byte("abc\ndef"))); got != "abc" {
		t.Errorf("FirstLine = %q, want abc", got)
	}
	if got := string(FirstLine([]byte("no newline"))); got != "no newline" {
		t.Errorf("FirstLine = %q, want whole slice", got)
	}
}

func TestSignatureSlice(t *testing.T) {
	tree := parseGo(t, greetSrc)
	defer tree.Close()
	fn := firstDescendant(tree.RootNode(), "function_declaration")
	params := fn.ChildByFieldName("parameters")
	got := strings.TrimSpace(SignatureSlice(greetSrc, fn, []*sitter.Node{params}))
	if got != "func Greet(name string)" {
		t.Errorf("SignatureSlice = %q", got)
	}

	// With no params node, it falls back to the def range (first line only).
	got = strings.TrimSpace(SignatureSlice(greetSrc, fn, nil))
	if got != "func Greet(name string) string {" {
		t.Errorf("SignatureSlice(no params) = %q", got)
	}
}

// ---- generic.go pure helpers ----------------------------------------------

func TestStripImportKeyword(t *testing.T) {
	tests := map[string]string{
		"import scala.collection.mutable": "scala.collection.mutable",
		"use std::collections":            "std::collections",
		"from os import path":             "os import path",
		"require 'set'":                   "'set'",
		"fmt":                             "fmt", // no keyword → unchanged
	}
	for in, want := range tests {
		if got := stripImportKeyword(in); got != want {
			t.Errorf("stripImportKeyword(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsRelative(t *testing.T) {
	prefixes := []string{".", ".."}
	if !isRelative("./local", prefixes) {
		t.Error("./local should be relative")
	}
	if !isRelative("../sibling", prefixes) {
		t.Error("../sibling should be relative")
	}
	if isRelative("fmt", prefixes) {
		t.Error("fmt should not be relative")
	}
	if isRelative("anything", nil) {
		t.Error("no prefixes → never relative")
	}
}

func TestAbsorbGenericImport(t *testing.T) {
	// Drive absorbGenericImport with captures pulled from a real parse.
	tree := parseGo(t, greetSrc)
	defer tree.Close()
	stmt := firstDescendant(tree.RootNode(), "import_spec")
	mod := firstDescendant(stmt, "interpreted_string_literal")
	caps := map[string][]*sitter.Node{
		"import.statement": {stmt},
		"import.module":    {mod},
	}

	// Plain config: quotes trimmed, not relative.
	got := absorbGenericImport(caps, greetSrc, GenericConfig{})
	if got == nil || got.ModulePath != "fmt" {
		t.Fatalf("absorbGenericImport = %+v, want ModulePath fmt", got)
	}
	if got.IsRelative {
		t.Error("fmt should not be relative")
	}

	// ImportKinds set but no import.kind capture → skipped (returns nil).
	cfg := GenericConfig{ImportKinds: map[string]struct{}{"require": {}}}
	if got := absorbGenericImport(caps, greetSrc, cfg); got != nil {
		t.Errorf("require-style absorber with no kind capture = %+v, want nil", got)
	}
}

// ---- GenericExtract end-to-end (generic.go) --------------------------------

// TestGenericExtract drives the high-level generic extractor through the real
// Ruby grammar + ruby.scm query — covering LoadQueryOnce, absorbGenericSymbol,
// applyModifiers, the require-style import filter, and the public-top-level
// export rule in one pass.
func TestGenericExtract(t *testing.T) {
	src := []byte(`require 'set'

class Greeter
  def hello(name)
    if name
      puts name
    end
  end
end

def standalone
end
`)
	var (
		q    *sitter.Query
		once sync.Once
		qErr error
	)
	cfg := GenericConfig{
		Lang: ruby.GetLanguage(), QueryName: "ruby.scm", LangTag: "ruby",
		Once: &once, QueryPtr: &q, ErrPtr: &qErr,
		KindFor: func(def *sitter.Node, _ []byte) models.SymbolKind {
			switch def.Type() {
			case "module":
				return models.KindModule
			case "class":
				return models.KindClass
			default:
				return models.KindFunction
			}
		},
		DefaultVis:       models.VisibilityPublic,
		ContainerTypes:   map[string]struct{}{"class": {}, "module": {}},
		BranchTypes:      map[string]struct{}{"if": {}},
		ImportKinds:      map[string]struct{}{"require": {}, "require_relative": {}},
		RelativePrefixes: []string{"./", "../"},
	}

	pf, err := GenericExtract(context.Background(), models.FileInfo{Path: "greeter.rb"}, src, cfg)
	if err != nil {
		t.Fatalf("GenericExtract: %v", err)
	}

	// Class Greeter is a top-level class.
	greeter := symByName(pf.Symbols, "Greeter")
	if greeter == nil || greeter.Kind != models.KindClass {
		t.Fatalf("Greeter symbol = %+v, want a class", greeter)
	}

	// hello is nested in Greeter → retagged as a method with ParentName set,
	// QualifiedName dotted, and a complexity bump from the `if`.
	hello := symByName(pf.Symbols, "hello")
	if hello == nil {
		t.Fatal("missing hello method")
	}
	if hello.Kind != models.KindMethod {
		t.Errorf("hello kind = %v, want method", hello.Kind)
	}
	if hello.ParentName == nil || *hello.ParentName != "Greeter" {
		t.Errorf("hello parent = %v, want Greeter", hello.ParentName)
	}
	if hello.QualifiedName != "Greeter.hello" {
		t.Errorf("hello qualified name = %q, want Greeter.hello", hello.QualifiedName)
	}
	if hello.ID != "greeter.rb::Greeter::hello" {
		t.Errorf("hello ID = %q", hello.ID)
	}
	if hello.ComplexityEstimate != 2 { // 1 + one `if`
		t.Errorf("hello complexity = %d, want 2", hello.ComplexityEstimate)
	}

	// require 'set' → an import (kind in ImportKinds); module quotes stripped.
	foundSet := false
	for _, im := range pf.Imports {
		if im.ModulePath == "set" {
			foundSet = true
			if im.IsRelative {
				t.Error("'set' should not be relative")
			}
		}
	}
	if !foundSet {
		t.Errorf("imports missing 'set': %+v", pf.Imports)
	}

	// Exports = public top-level symbols: Greeter and standalone, never the
	// nested method hello.
	if !slices.Contains(pf.Exports, "Greeter") || !slices.Contains(pf.Exports, "standalone") {
		t.Errorf("exports = %v, want to contain Greeter and standalone", pf.Exports)
	}
	if slices.Contains(pf.Exports, "hello") {
		t.Errorf("exports = %v, should not contain nested method hello", pf.Exports)
	}
}

func TestApplyModifiers(t *testing.T) {
	// Each modifier word is parsed as an identifier so we can feed a real node
	// (with source-backed Content) into applyModifiers as a symbol.modifiers
	// capture.
	src := []byte("package p\nvar public, private, protected, internal, pub int\n")
	tree := parseGo(t, src)
	defer tree.Close()

	byContent := map[string]*sitter.Node{}
	stack := []*sitter.Node{tree.RootNode()}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.Type() == "identifier" {
			byContent[n.Content(src)] = n
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			stack = append(stack, n.NamedChild(i))
		}
	}

	tests := []struct {
		word string
		want models.Visibility
	}{
		{"public", models.VisibilityPublic},
		{"private", models.VisibilityPrivate},
		{"protected", models.VisibilityProtected},
		{"internal", models.VisibilityInternal},
		{"pub", models.VisibilityPublic}, // Rust-style "pub" prefix
	}
	for _, tc := range tests {
		node := byContent[tc.word]
		if node == nil {
			t.Fatalf("no identifier node for %q", tc.word)
		}
		sym := &models.Symbol{Visibility: models.VisibilityPrivate}
		applyModifiers(sym, map[string][]*sitter.Node{"symbol.modifiers": {node}}, src)
		if sym.Visibility != tc.want {
			t.Errorf("modifier %q → visibility %v, want %v", tc.word, sym.Visibility, tc.want)
		}
	}

	// No modifiers capture → visibility untouched.
	sym := &models.Symbol{Visibility: models.VisibilityInternal}
	applyModifiers(sym, map[string][]*sitter.Node{}, src)
	if sym.Visibility != models.VisibilityInternal {
		t.Errorf("no modifiers changed visibility to %v", sym.Visibility)
	}
}

func TestGenericExtract_QueryError(t *testing.T) {
	// A non-existent query name surfaces as an error, not a panic.
	var (
		q    *sitter.Query
		once sync.Once
		qErr error
	)
	_, err := GenericExtract(context.Background(), models.FileInfo{Path: "x.rb"}, []byte("x = 1"), GenericConfig{
		Lang: ruby.GetLanguage(), QueryName: "does-not-exist.scm",
		Once: &once, QueryPtr: &q, ErrPtr: &qErr,
		KindFor:    func(*sitter.Node, []byte) models.SymbolKind { return models.KindFunction },
		DefaultVis: models.VisibilityPublic,
	})
	if err == nil {
		t.Fatal("GenericExtract with missing query returned nil error")
	}
}

// ---- small utilities -------------------------------------------------------

func TestPtrHelpers(t *testing.T) {
	if got := PtrStr("x"); got == nil || *got != "x" {
		t.Errorf("PtrStr = %v", got)
	}
	if got := PtrInt(7); got == nil || *got != 7 {
		t.Errorf("PtrInt = %v", got)
	}
}

// ---- local test helpers ----------------------------------------------------

func ptr(s string) *string { return &s }

