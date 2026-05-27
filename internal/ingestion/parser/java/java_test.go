package java

import (
	"context"
	"slices"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

func TestParse_Java(t *testing.T) {
	src := []byte(`package com.example;

import java.util.List;
import java.util.HashMap;

public class Calculator {
    private int base;

    public Calculator(int base) {
        this.base = base;
    }

    public int add(int a, int b) {
        if (a > 0) {
            return a + b + this.base;
        }
        return b;
    }

    private void secret() {}
}

interface Greeter {
    String greet();
}

public enum Status {
    ACTIVE, INACTIVE;
}

public record Point(double x, double y) {}
`)
	fi := models.FileInfo{Path: "src/Calculator.java", Language: "java"}
	parsed, err := (&Parser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	names := make([]string, 0, len(parsed.Symbols))
	for _, s := range parsed.Symbols {
		names = append(names, s.Name)
	}
	for _, want := range []string{"Calculator", "add", "secret", "Greeter", "Status", "Point"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing symbol %q in %v", want, names)
		}
	}

	for _, s := range parsed.Symbols {
		switch {
		case s.Name == "Calculator" && s.ParentName == nil:
			if s.Kind != models.KindClass {
				t.Errorf("Calculator (class) kind = %v, want class", s.Kind)
			}
			if s.Visibility != models.VisibilityPublic {
				t.Errorf("Calculator visibility = %v, want public", s.Visibility)
			}
		}
		switch s.Name {
		case "add":
			if s.Kind != models.KindMethod {
				t.Errorf("add kind = %v, want method", s.Kind)
			}
			if s.ParentName == nil || *s.ParentName != "Calculator" {
				t.Errorf("add parent = %v, want Calculator", s.ParentName)
			}
			if s.Visibility != models.VisibilityPublic {
				t.Errorf("add visibility = %v, want public", s.Visibility)
			}
			if s.ComplexityEstimate < 2 {
				t.Errorf("add complexity = %d, want >= 2", s.ComplexityEstimate)
			}
		case "secret":
			if s.Visibility != models.VisibilityPrivate {
				t.Errorf("secret visibility = %v, want private", s.Visibility)
			}
		case "Greeter":
			if s.Kind != models.KindInterface {
				t.Errorf("Greeter kind = %v, want interface", s.Kind)
			}
			// Package-private (no modifier).
			if s.Visibility != models.VisibilityInternal {
				t.Errorf("Greeter visibility = %v, want internal (package-private)", s.Visibility)
			}
		case "Status":
			if s.Kind != models.KindEnum {
				t.Errorf("Status kind = %v, want enum", s.Kind)
			}
		case "Point":
			if s.Kind != models.KindStruct {
				t.Errorf("Point kind = %v, want struct (record)", s.Kind)
			}
		}
	}

	gotImports := []string{}
	for _, im := range parsed.Imports {
		gotImports = append(gotImports, im.ModulePath)
	}
	for _, want := range []string{"java.util.List", "java.util.HashMap"} {
		if !slices.Contains(gotImports, want) {
			t.Errorf("missing import %q in %v", want, gotImports)
		}
	}

	// Top-level exports = public types only.
	for _, w := range []string{"Calculator", "Status", "Point"} {
		if !slices.Contains(parsed.Exports, w) {
			t.Errorf("missing export %q in %v", w, parsed.Exports)
		}
	}
	if slices.Contains(parsed.Exports, "Greeter") {
		t.Errorf("package-private Greeter leaked into exports")
	}
}

func TestParse_JavaAnnotationDecorators(t *testing.T) {
	src := []byte(`package x;

public class C {
    @Override
    public String toString() {
        return "C";
    }
}
`)
	parsed, err := (&Parser{}).Parse(context.Background(),
		models.FileInfo{Path: "C.java", Language: "java"}, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, s := range parsed.Symbols {
		if s.Name == "toString" {
			if !slices.Contains(s.Decorators, "@Override") {
				t.Errorf("toString missing @Override decorator: %v", s.Decorators)
			}
		}
	}
}
