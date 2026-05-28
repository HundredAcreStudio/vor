package python

import (
	"context"
	"slices"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

func TestParse_SimpleModule(t *testing.T) {
	src := []byte(`"""A demo module."""

import os
from typing import List, Optional

def greet(name: str) -> str:
    """Say hello."""
    return f"hello {name}"

async def fetch(url: str) -> str:
    return await get(url)

def _internal():
    pass

class Calculator:
    """Adds and subtracts."""

    def add(self, a: int, b: int) -> int:
        return a + b

    def _private_helper(self):
        return None

    @staticmethod
    def static_thing():
        return 42

@deprecated
def old_api():
    pass
`)
	fi := models.FileInfo{Path: "demo/calc.py", Language: "python"}
	parsed, err := (&Parser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	names := symbolNames(parsed.Symbols)
	for _, want := range []string{"greet", "fetch", "_internal", "Calculator", "add", "_private_helper", "static_thing", "old_api"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing symbol %q in %v", want, names)
		}
	}

	for _, s := range parsed.Symbols {
		switch s.Name {
		case "greet":
			if s.Kind != models.KindFunction {
				t.Errorf("greet kind = %v, want function", s.Kind)
			}
			if s.Visibility != models.VisibilityPublic {
				t.Errorf("greet visibility = %v, want public", s.Visibility)
			}
			if s.IsAsync {
				t.Errorf("greet IsAsync = true, want false")
			}
		case "fetch":
			if !s.IsAsync {
				t.Errorf("fetch IsAsync = false, want true")
			}
		case "_internal":
			if s.Visibility != models.VisibilityProtected {
				t.Errorf("_internal visibility = %v, want protected", s.Visibility)
			}
		case "Calculator":
			if s.Kind != models.KindClass {
				t.Errorf("Calculator kind = %v, want class", s.Kind)
			}
		case "add":
			if s.Kind != models.KindMethod {
				t.Errorf("add kind = %v, want method", s.Kind)
			}
			if s.ParentName == nil || *s.ParentName != "Calculator" {
				t.Errorf("add parent = %v, want Calculator", s.ParentName)
			}
			if s.ID != "demo/calc.py::Calculator::add" {
				t.Errorf("add ID = %q", s.ID)
			}
			if s.QualifiedName != "Calculator.add" {
				t.Errorf("add QualifiedName = %q", s.QualifiedName)
			}
		case "_private_helper":
			if s.Visibility != models.VisibilityProtected {
				t.Errorf("_private_helper visibility = %v, want protected", s.Visibility)
			}
		case "static_thing":
			if len(s.Decorators) == 0 {
				t.Errorf("static_thing decorators = empty, want @staticmethod")
			}
		case "old_api":
			if !slices.ContainsFunc(s.Decorators, func(d string) bool { return d == "@deprecated" }) {
				t.Errorf("old_api decorators = %v, want to contain @deprecated", s.Decorators)
			}
		}
	}

	// Imports.
	wantImports := []string{"os", "typing"}
	got := importModules(parsed.Imports)
	for _, w := range wantImports {
		if !slices.Contains(got, w) {
			t.Errorf("missing import %q in %v", w, got)
		}
	}

	// Module docstring.
	if parsed.Docstring == nil || *parsed.Docstring != "A demo module." {
		t.Errorf("Docstring = %v, want \"A demo module.\"", parsed.Docstring)
	}

	// Exports: top-level public only (greet, fetch, Calculator, old_api).
	// Methods inside Calculator are not exports.
	for _, w := range []string{"greet", "fetch", "Calculator", "old_api"} {
		if !slices.Contains(parsed.Exports, w) {
			t.Errorf("missing export %q in %v", w, parsed.Exports)
		}
	}
	if slices.Contains(parsed.Exports, "add") {
		t.Errorf("add (method) leaked into exports: %v", parsed.Exports)
	}
	if slices.Contains(parsed.Exports, "_internal") {
		t.Errorf("_internal leaked into exports: %v", parsed.Exports)
	}
}

func TestParse_PythonComplexity(t *testing.T) {
	src := []byte(`def trivial():
    return 1

def one(n):
    if n > 0:
        return n
    return 0

def four(n):
    if n > 10:
        return 1
    elif n > 5:
        return 2
    for i in range(n):
        pass
    return 0
`)
	fi := models.FileInfo{Path: "x/x.py", Language: "python"}
	parsed, err := (&Parser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := map[string]int{
		"trivial": 1,
		"one":     2,
		"four":    4, // if + elif + for
	}
	for _, s := range parsed.Symbols {
		if expect, ok := want[s.Name]; ok && s.ComplexityEstimate != expect {
			t.Errorf("%s ComplexityEstimate = %d, want %d", s.Name, s.ComplexityEstimate, expect)
		}
	}
}

func TestPythonVisibility(t *testing.T) {
	cases := []struct {
		name string
		want models.Visibility
	}{
		{"name", models.VisibilityPublic},
		{"_name", models.VisibilityProtected},
		{"__name", models.VisibilityPrivate},
		{"__init__", models.VisibilityPublic}, // dunder
	}
	for _, tc := range cases {
		if got := pythonVisibility(tc.name); got != tc.want {
			t.Errorf("pythonVisibility(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func symbolNames(s []models.Symbol) []string {
	out := make([]string, 0, len(s))
	for _, sy := range s {
		out = append(out, sy.Name)
	}
	return out
}

func importModules(imps []models.Import) []string {
	out := make([]string, 0, len(imps))
	for _, im := range imps {
		out = append(out, im.ModulePath)
	}
	return out
}
