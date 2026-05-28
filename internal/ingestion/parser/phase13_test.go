package parser_test

import (
	"context"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser"

	// Side-effect imports register the Phase 13 parsers.
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/kotlin"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/luau"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/php"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/ruby"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/scala"
	_ "github.com/HundredAcreStudio/vor/internal/ingestion/parser/swift"
)

func hasSymbol(syms []models.Symbol, name string) *models.Symbol {
	for i := range syms {
		if syms[i].Name == name {
			return &syms[i]
		}
	}
	return nil
}

func hasImport(imps []models.Import, substr string) bool {
	for _, im := range imps {
		if im.ModulePath != "" && contains(im.ModulePath, substr) {
			return true
		}
	}
	return false
}

func hasCall(calls []models.CallSite, target string) bool {
	for _, c := range calls {
		if c.TargetName == target {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestPhase13Parsers(t *testing.T) {
	cases := []struct {
		name      string
		lang      models.LanguageTag
		file      string
		src       string
		wantSyms  []string
		wantImp   string
		wantCall  string
		checkKind func(t *testing.T, pf models.ParsedFile)
	}{
		{
			name: "ruby", lang: "ruby", file: "calc.rb",
			src: `require 'json'
module MyMod
  class Calculator
    def add(a, b)
      helper(a)
    end
  end
end`,
			wantSyms: []string{"MyMod", "Calculator", "add"}, wantImp: "json", wantCall: "helper",
			checkKind: func(t *testing.T, pf models.ParsedFile) {
				if s := hasSymbol(pf.Symbols, "add"); s == nil || s.Kind != models.KindMethod {
					t.Errorf("ruby: add should be a method, got %+v", s)
				}
			},
		},
		{
			name: "php", lang: "php", file: "Calc.php",
			src: `<?php
namespace App;
use App\Foo\Bar;
class Calculator {
  public function add($a, $b) {
    return $this->helper($a);
  }
}
function standalone($x) {
  return doThing($x);
}`,
			wantSyms: []string{"Calculator", "add", "standalone"}, wantImp: "Bar", wantCall: "doThing",
			checkKind: func(t *testing.T, pf models.ParsedFile) {
				if s := hasSymbol(pf.Symbols, "add"); s == nil || s.Kind != models.KindMethod {
					t.Errorf("php: add should be a method, got %+v", s)
				}
			},
		},
		{
			name: "swift", lang: "swift", file: "Calc.swift",
			src: `import Foundation
class Calculator {
    func add(a: Int) -> Int {
        return helper(a)
    }
}
func standalone(x: Int) { obj.process() }
struct Point { var x: Int }
protocol Shape { }`,
			wantSyms: []string{"Calculator", "add", "standalone", "Point", "Shape"}, wantImp: "Foundation", wantCall: "helper",
			checkKind: func(t *testing.T, pf models.ParsedFile) {
				if s := hasSymbol(pf.Symbols, "Point"); s == nil || s.Kind != models.KindStruct {
					t.Errorf("swift: Point should be a struct, got %+v", s)
				}
				if s := hasSymbol(pf.Symbols, "Shape"); s == nil || s.Kind != models.KindInterface {
					t.Errorf("swift: Shape should be an interface (protocol), got %+v", s)
				}
			},
		},
		{
			name: "kotlin", lang: "kotlin", file: "Calc.kt",
			src: `package com.example
import kotlin.math.max
class Calculator {
    fun add(a: Int): Int { return helper(a) }
}
fun standalone(x: Int) { obj.process() }`,
			wantSyms: []string{"Calculator", "add", "standalone"}, wantImp: "max", wantCall: "helper",
		},
		{
			name: "scala", lang: "scala", file: "Calc.scala",
			src: `package com.example
import scala.collection.mutable
class Calculator {
  def add(a: Int): Int = { helper(a) }
}
object Main { def run(): Unit = process() }
trait Shape`,
			wantSyms: []string{"Calculator", "add", "Main", "Shape"}, wantImp: "scala.collection.mutable", wantCall: "helper",
			checkKind: func(t *testing.T, pf models.ParsedFile) {
				if s := hasSymbol(pf.Symbols, "Shape"); s == nil || s.Kind != models.KindTrait {
					t.Errorf("scala: Shape should be a trait, got %+v", s)
				}
			},
		},
		{
			name: "luau", lang: "luau", file: "calc.luau",
			src: `local mod = require("mymod")
local function standalone(x)
  return helper(x)
end
function Calculator.add(a, b)
  return process(a)
end`,
			wantSyms: []string{"standalone"}, wantImp: "mymod", wantCall: "helper",
		},
	}

	ap := parser.New()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if parser.LookupParser(c.lang) == nil {
				t.Fatalf("%s parser not registered", c.lang)
			}
			fi := models.FileInfo{Path: c.file, Language: c.lang}
			pf, err := ap.Parse(context.Background(), fi, []byte(c.src))
			if err != nil {
				t.Fatalf("%s parse: %v", c.name, err)
			}
			for _, want := range c.wantSyms {
				if hasSymbol(pf.Symbols, want) == nil {
					t.Errorf("%s: expected symbol %q; got %v", c.name, want, symbolNames(pf.Symbols))
				}
			}
			if c.wantImp != "" && !hasImport(pf.Imports, c.wantImp) {
				t.Errorf("%s: expected an import containing %q; got %v", c.name, c.wantImp, importPaths(pf.Imports))
			}
			if c.wantCall != "" && !hasCall(pf.Calls, c.wantCall) {
				t.Errorf("%s: expected a call to %q; got %v", c.name, c.wantCall, callTargets(pf.Calls))
			}
			if c.checkKind != nil {
				c.checkKind(t, pf)
			}
		})
	}
}

func symbolNames(syms []models.Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = string(s.Kind) + ":" + s.Name
	}
	return out
}

func importPaths(imps []models.Import) []string {
	out := make([]string, len(imps))
	for i, im := range imps {
		out[i] = im.ModulePath
	}
	return out
}

func callTargets(calls []models.CallSite) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.TargetName
	}
	return out
}
