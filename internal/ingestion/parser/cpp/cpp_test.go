package cpp

import (
	"context"
	"slices"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

func TestCpp_FunctionsAndClasses(t *testing.T) {
	src := []byte(`#include <iostream>
#include "local.h"

namespace demo {

class Calculator {
public:
    Calculator(int base) : base_(base) {}

    int add(int a, int b) {
        if (a > 0) {
            return a + b + base_;
        }
        return b;
    }

private:
    int base_;
};

int greet(const char* name) {
    std::cout << "hello " << name << std::endl;
    return 0;
}

}  // namespace demo
`)
	fi := models.FileInfo{Path: "src/demo.cpp", Language: "cpp"}
	parsed, err := (&CppParser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	names := []string{}
	for _, s := range parsed.Symbols {
		names = append(names, s.Name)
	}
	for _, want := range []string{"Calculator", "add", "greet", "demo"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing symbol %q in %v", want, names)
		}
	}
	for _, s := range parsed.Symbols {
		switch s.Name {
		case "Calculator":
			// Class declaration + same-named constructor both produce a
			// Calculator symbol; either kind is acceptable.
			if s.Kind != models.KindClass && s.Kind != models.KindFunction && s.Kind != models.KindMethod {
				t.Errorf("Calculator kind = %v, want class/function/method", s.Kind)
			}
		case "add":
			if s.Kind != models.KindMethod {
				t.Errorf("add kind = %v, want method", s.Kind)
			}
			if s.ParentName == nil || *s.ParentName != "Calculator" {
				t.Errorf("add parent = %v, want Calculator", s.ParentName)
			}
			if s.ComplexityEstimate < 2 {
				t.Errorf("add complexity = %d, want >= 2", s.ComplexityEstimate)
			}
		case "greet":
			if s.Kind != models.KindFunction {
				t.Errorf("greet kind = %v, want function", s.Kind)
			}
		case "demo":
			if s.Kind != models.KindModule {
				t.Errorf("demo kind = %v, want module (namespace)", s.Kind)
			}
		}
	}

	wantImports := []string{"iostream", "local.h"}
	got := []string{}
	for _, im := range parsed.Imports {
		got = append(got, im.ModulePath)
	}
	for _, w := range wantImports {
		if !slices.Contains(got, w) {
			t.Errorf("missing import %q in %v", w, got)
		}
	}
}

func TestC_FunctionsAndIncludes(t *testing.T) {
	src := []byte(`#include <stdio.h>
#include "util.h"

int add(int a, int b) {
    return a + b;
}

int main(void) {
    printf("%d\n", add(2, 3));
    return 0;
}
`)
	fi := models.FileInfo{Path: "src/main.c", Language: "c"}
	parsed, err := (&CParser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	names := []string{}
	for _, s := range parsed.Symbols {
		names = append(names, s.Name)
	}
	for _, want := range []string{"add", "main"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing %q in %v", want, names)
		}
	}
	// printf, add appear as calls inside main.
	targets := []string{}
	for _, c := range parsed.Calls {
		targets = append(targets, c.TargetName)
	}
	for _, t2 := range []string{"printf", "add"} {
		if !slices.Contains(targets, t2) {
			t.Errorf("missing call target %q in %v", t2, targets)
		}
	}
}
