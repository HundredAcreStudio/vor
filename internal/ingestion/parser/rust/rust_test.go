package rust

import (
	"context"
	"slices"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

func TestParse_Rust(t *testing.T) {
	src := []byte(`use std::collections::HashMap;
use crate::lib::helper;

pub fn greet(name: &str) -> String {
    format!("hello {}", name)
}

fn private_helper() {}

pub struct Calculator {
    base: i32,
}

impl Calculator {
    pub fn new(base: i32) -> Self {
        Calculator { base }
    }

    pub fn add(&self, a: i32, b: i32) -> i32 {
        if a > 0 {
            return a + b + self.base;
        }
        b
    }
}

pub trait Greeter {
    fn greet(&self) -> String;
}

pub enum Status {
    Active,
    Inactive,
}

pub const MAX_RETRIES: u32 = 3;
`)
	fi := models.FileInfo{Path: "src/demo.rs", Language: "rust"}
	parsed, err := (&Parser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	names := make([]string, 0, len(parsed.Symbols))
	for _, s := range parsed.Symbols {
		names = append(names, s.Name)
	}
	for _, want := range []string{"greet", "private_helper", "Calculator", "new", "add", "Greeter", "Status", "MAX_RETRIES"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing symbol %q in %v", want, names)
		}
	}

	for _, s := range parsed.Symbols {
		switch s.Name {
		case "greet":
			if s.Visibility != models.VisibilityPublic {
				t.Errorf("greet visibility = %v, want public", s.Visibility)
			}
		case "private_helper":
			if s.Visibility != models.VisibilityPrivate {
				t.Errorf("private_helper visibility = %v, want private", s.Visibility)
			}
		case "Calculator":
			// Two symbols share the name: the struct declaration and the
			// impl block. Either Kind is acceptable here.
			if s.Kind != models.KindStruct && s.Kind != models.KindImpl {
				t.Errorf("Calculator kind = %v, want struct or impl", s.Kind)
			}
		case "add":
			if s.Kind != models.KindMethod {
				t.Errorf("add kind = %v, want method", s.Kind)
			}
			if s.ParentName == nil || *s.ParentName != "Calculator" {
				t.Errorf("add parent = %v, want Calculator", s.ParentName)
			}
			if s.QualifiedName != "Calculator::add" {
				t.Errorf("add qualified name = %v, want Calculator::add", s.QualifiedName)
			}
			if s.ComplexityEstimate < 2 {
				t.Errorf("add complexity = %d, want >= 2 (has if)", s.ComplexityEstimate)
			}
		case "Greeter":
			if s.Kind != models.KindTrait {
				t.Errorf("Greeter kind = %v, want trait", s.Kind)
			}
		case "Status":
			if s.Kind != models.KindEnum {
				t.Errorf("Status kind = %v, want enum", s.Kind)
			}
		case "MAX_RETRIES":
			if s.Kind != models.KindConstant {
				t.Errorf("MAX_RETRIES kind = %v, want constant", s.Kind)
			}
		}
	}

	// Imports.
	got := []string{}
	for _, im := range parsed.Imports {
		got = append(got, im.ModulePath)
	}
	for _, w := range []string{"std::collections::HashMap", "crate::lib::helper"} {
		if !slices.Contains(got, w) {
			t.Errorf("missing import %q in %v", w, got)
		}
	}

	// Exports — top-level pub symbols only.
	for _, w := range []string{"greet", "Calculator", "Greeter", "Status", "MAX_RETRIES"} {
		if !slices.Contains(parsed.Exports, w) {
			t.Errorf("missing export %q in %v", w, parsed.Exports)
		}
	}
	if slices.Contains(parsed.Exports, "private_helper") {
		t.Errorf("private_helper leaked into exports: %v", parsed.Exports)
	}
	// Methods aren't exports.
	if slices.Contains(parsed.Exports, "add") {
		t.Errorf("add (method) leaked into exports: %v", parsed.Exports)
	}
}

func TestParse_RustMacroInvocationsAreCalls(t *testing.T) {
	src := []byte(`fn main() {
    println!("hello");
    let v = vec![1, 2, 3];
    let _ = v;
}
`)
	fi := models.FileInfo{Path: "main.rs", Language: "rust"}
	parsed, err := (&Parser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	targets := []string{}
	for _, c := range parsed.Calls {
		targets = append(targets, c.TargetName)
	}
	for _, want := range []string{"println", "vec"} {
		if !slices.Contains(targets, want) {
			t.Errorf("missing macro call %q in %v", want, targets)
		}
	}
}
