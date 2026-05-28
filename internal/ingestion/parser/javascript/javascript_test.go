package javascript

import (
	"context"
	"slices"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

func TestParse_JavaScript(t *testing.T) {
	src := []byte(`import { useState } from "react";

export function greet(name) {
  return "hello " + name;
}

export const square = (n) => n * n;

function unexported() {}

export class Calculator {
  constructor(base) { this.base = base; }
  add(a, b) {
    if (a > 0) return a + b + this.base;
    return b;
  }
}
`)
	fi := models.FileInfo{Path: "src/demo.js", Language: "javascript"}
	parsed, err := (&Parser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	names := make([]string, 0, len(parsed.Symbols))
	for _, s := range parsed.Symbols {
		names = append(names, s.Name)
	}
	for _, want := range []string{"greet", "square", "unexported", "Calculator", "constructor", "add"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing symbol %q in %v", want, names)
		}
	}
	for _, s := range parsed.Symbols {
		if s.Name == "add" {
			if s.Kind != models.KindMethod || s.ParentName == nil || *s.ParentName != "Calculator" {
				t.Errorf("add expected method-of-Calculator: %+v", s)
			}
			if s.ComplexityEstimate < 2 {
				t.Errorf("add complexity = %d, want >= 2", s.ComplexityEstimate)
			}
		}
		if s.Name == "greet" && !s.IsExportedSymbol {
			t.Errorf("greet should be exported")
		}
		if s.Name == "unexported" && s.IsExportedSymbol {
			t.Errorf("unexported should not be exported")
		}
	}
	if len(parsed.Imports) != 1 || parsed.Imports[0].ModulePath != "react" {
		t.Errorf("imports = %+v", parsed.Imports)
	}
}

func TestParse_JSXCallsExtracted(t *testing.T) {
	src := []byte(`function Page() {
  return <Header><Footer /></Header>;
}
`)
	fi := models.FileInfo{Path: "page.jsx", Language: "javascript"}
	parsed, err := (&Parser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	callTargets := make([]string, 0, len(parsed.Calls))
	for _, c := range parsed.Calls {
		callTargets = append(callTargets, c.TargetName)
	}
	for _, want := range []string{"Header", "Footer"} {
		if !slices.Contains(callTargets, want) {
			t.Errorf("missing JSX call target %q in %v", want, callTargets)
		}
	}
}
