package csharp

import (
	"context"
	"slices"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

func TestParse_CSharp(t *testing.T) {
	src := []byte(`using System;
using System.Collections.Generic;

namespace Demo
{
    public class Calculator
    {
        private int base_;

        public Calculator(int base_)
        {
            this.base_ = base_;
        }

        public int Add(int a, int b)
        {
            if (a > 0)
            {
                return a + b + this.base_;
            }
            return b;
        }

        private void Secret() { }
    }

    public interface IGreeter
    {
        string Greet();
    }

    public enum Status
    {
        Active,
        Inactive,
    }
}
`)
	fi := models.FileInfo{Path: "src/Calculator.cs", Language: "csharp"}
	parsed, err := (&Parser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	names := []string{}
	for _, s := range parsed.Symbols {
		names = append(names, s.Name)
	}
	for _, want := range []string{"Calculator", "Add", "Secret", "IGreeter", "Status"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing %q in %v", want, names)
		}
	}
	for _, s := range parsed.Symbols {
		switch s.Name {
		case "Calculator":
			// Class declaration + same-named constructor both produce a
			// Calculator symbol; either kind is acceptable.
			if s.Kind != models.KindClass && s.Kind != models.KindFunction && s.Kind != models.KindMethod {
				t.Errorf("Calculator kind = %v", s.Kind)
			}
		case "Add":
			if s.Kind != models.KindMethod {
				t.Errorf("Add kind = %v, want method", s.Kind)
			}
			if s.ParentName == nil || *s.ParentName != "Calculator" {
				t.Errorf("Add parent = %v, want Calculator", s.ParentName)
			}
			if s.Visibility != models.VisibilityPublic {
				t.Errorf("Add visibility = %v, want public", s.Visibility)
			}
			if s.ComplexityEstimate < 2 {
				t.Errorf("Add complexity = %d, want >= 2", s.ComplexityEstimate)
			}
		case "Secret":
			if s.Visibility != models.VisibilityPrivate {
				t.Errorf("Secret visibility = %v, want private", s.Visibility)
			}
		case "IGreeter":
			if s.Kind != models.KindInterface {
				t.Errorf("IGreeter kind = %v, want interface", s.Kind)
			}
		case "Status":
			if s.Kind != models.KindEnum {
				t.Errorf("Status kind = %v, want enum", s.Kind)
			}
		}
	}

	got := []string{}
	for _, im := range parsed.Imports {
		got = append(got, im.ModulePath)
	}
	for _, want := range []string{"System", "System.Collections.Generic"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing import %q in %v", want, got)
		}
	}
}

func TestParse_FileScopedNamespace(t *testing.T) {
	// C# 10+ file-scoped namespace syntax.
	src := []byte(`namespace Demo;

public class Foo { }
`)
	parsed, err := (&Parser{}).Parse(context.Background(),
		models.FileInfo{Path: "Foo.cs", Language: "csharp"}, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	names := []string{}
	for _, s := range parsed.Symbols {
		names = append(names, s.Name)
	}
	for _, want := range []string{"Demo", "Foo"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing %q in %v", want, names)
		}
	}
}
