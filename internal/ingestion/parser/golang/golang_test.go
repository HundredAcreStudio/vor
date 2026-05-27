package golang

import (
	"context"
	"slices"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

func TestParse_SimpleFile(t *testing.T) {
	src := []byte(`package demo

import (
	"fmt"
	"strings"
)

// Greet returns a hello string for n.
func Greet(name string) string {
	return fmt.Sprintf("hello %s", strings.ToUpper(name))
}

func unexported() {}

type User struct {
	Name string
}

func (u *User) Hello() string {
	return Greet(u.Name)
}

const MaxRetries = 3
var DefaultName = "world"
`)
	fi := models.FileInfo{Path: "demo/demo.go", Language: "go"}
	parsed, err := (&Parser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Symbols: Greet, unexported, User, User.Hello, MaxRetries, DefaultName.
	names := symNames(parsed.Symbols)
	for _, want := range []string{"Greet", "unexported", "User", "Hello", "MaxRetries", "DefaultName"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing symbol %q in %v", want, names)
		}
	}

	// Greet is a function; Hello is a method with parent User.
	for _, s := range parsed.Symbols {
		switch s.Name {
		case "Greet":
			if s.Kind != models.KindFunction {
				t.Errorf("Greet kind = %v, want function", s.Kind)
			}
			if s.Visibility != models.VisibilityPublic {
				t.Errorf("Greet visibility = %v, want public", s.Visibility)
			}
			if !s.IsExportedSymbol {
				t.Errorf("Greet IsExportedSymbol = false")
			}
			if s.ID != "demo/demo.go::Greet" {
				t.Errorf("Greet ID = %q", s.ID)
			}
		case "unexported":
			if s.Visibility != models.VisibilityPrivate {
				t.Errorf("unexported visibility = %v, want private", s.Visibility)
			}
		case "Hello":
			if s.Kind != models.KindMethod {
				t.Errorf("Hello kind = %v, want method", s.Kind)
			}
			if s.ParentName == nil || *s.ParentName != "User" {
				t.Errorf("Hello parent = %v, want User", s.ParentName)
			}
			if s.ID != "demo/demo.go::User::Hello" {
				t.Errorf("Hello ID = %q", s.ID)
			}
		case "MaxRetries":
			if s.Kind != models.KindConstant {
				t.Errorf("MaxRetries kind = %v, want constant", s.Kind)
			}
		case "DefaultName":
			if s.Kind != models.KindVariable {
				t.Errorf("DefaultName kind = %v, want variable", s.Kind)
			}
		}
	}

	// Imports: fmt, strings.
	importPaths := make([]string, 0, len(parsed.Imports))
	for _, im := range parsed.Imports {
		importPaths = append(importPaths, im.ModulePath)
	}
	for _, want := range []string{"fmt", "strings"} {
		if !slices.Contains(importPaths, want) {
			t.Errorf("missing import %q in %v", want, importPaths)
		}
	}

	// Calls: fmt.Sprintf, strings.ToUpper, Greet.
	callTargets := make([]string, 0, len(parsed.Calls))
	for _, c := range parsed.Calls {
		callTargets = append(callTargets, c.TargetName)
	}
	for _, want := range []string{"Sprintf", "ToUpper", "Greet"} {
		if !slices.Contains(callTargets, want) {
			t.Errorf("missing call target %q in %v", want, callTargets)
		}
	}

	// CallerSymbolID assignment: fmt.Sprintf call lives inside Greet.
	for _, c := range parsed.Calls {
		if c.TargetName == "Sprintf" {
			if c.CallerSymbolID == nil || *c.CallerSymbolID != "demo/demo.go::Greet" {
				t.Errorf("Sprintf call caller = %v, want demo/demo.go::Greet", c.CallerSymbolID)
			}
		}
		if c.TargetName == "Greet" && c.ReceiverName == nil {
			// Hello calls Greet (bare identifier), should be inside Hello.
			if c.CallerSymbolID == nil || *c.CallerSymbolID != "demo/demo.go::User::Hello" {
				t.Errorf("Greet call caller = %v", c.CallerSymbolID)
			}
		}
	}

	// Exports: Greet, User, Hello, MaxRetries, DefaultName (uppercase).
	for _, want := range []string{"Greet", "User", "Hello", "MaxRetries", "DefaultName"} {
		if !slices.Contains(parsed.Exports, want) {
			t.Errorf("missing export %q in %v", want, parsed.Exports)
		}
	}

	// unexported should NOT be in exports.
	if slices.Contains(parsed.Exports, "unexported") {
		t.Errorf("unexported leaked into exports: %v", parsed.Exports)
	}
}

func TestParse_EmptyFile(t *testing.T) {
	src := []byte("package empty\n")
	fi := models.FileInfo{Path: "empty.go", Language: "go"}
	parsed, err := (&Parser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(parsed.Symbols) != 0 {
		t.Errorf("empty file produced symbols: %v", parsed.Symbols)
	}
	if len(parsed.Imports) != 0 {
		t.Errorf("empty file produced imports: %v", parsed.Imports)
	}
}

func TestParse_ComplexityCounts(t *testing.T) {
	src := []byte(`package x

// trivial — complexity 1
func trivial() int { return 1 }

// one if — complexity 2
func one(n int) int {
	if n > 0 {
		return n
	}
	return 0
}

// 2 ifs + 1 for = complexity 4
func three(n int) int {
	if n > 10 {
		return 1
	}
	if n > 5 {
		return 2
	}
	for i := 0; i < n; i++ {
		_ = i
	}
	return 0
}

// switch with 3 cases + default — complexity 4
func sw(n int) int {
	switch n {
	case 1:
		return 1
	case 2:
		return 2
	case 3:
		return 3
	default:
		return 0
	}
}
`)
	fi := models.FileInfo{Path: "x/x.go", Language: "go"}
	parsed, err := (&Parser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := map[string]int{
		"trivial": 1,
		"one":     2,
		"three":   4,
		"sw":      4,
	}
	for _, s := range parsed.Symbols {
		if expect, ok := want[s.Name]; ok && s.ComplexityEstimate != expect {
			t.Errorf("%s ComplexityEstimate = %d, want %d", s.Name, s.ComplexityEstimate, expect)
		}
	}
}

func TestGoReceiverType(t *testing.T) {
	cases := []struct{ in, want string }{
		{"(r *MyType)", "MyType"},
		{"(MyType)", "MyType"},
		{"(*MyType)", "MyType"},
		{"(r MyType[T])", "MyType"},
		{"(s *Server[K, V])", "Server"},
	}
	for _, tc := range cases {
		got := goReceiverType(tc.in)
		if got != tc.want {
			t.Errorf("goReceiverType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func symNames(s []models.Symbol) []string {
	out := make([]string, 0, len(s))
	for _, sy := range s {
		out = append(out, sy.Name)
	}
	return out
}
