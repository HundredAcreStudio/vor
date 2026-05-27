package languages

import "testing"

func TestDetectFromPath_ByExtension(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"src/foo.py", "python"},
		{"src/Foo.PY", "python"},
		{"app/index.ts", "typescript"},
		{"app/Component.tsx", "typescript"},
		{"main.go", "go"},
		{"src/foo.rs", "rust"},
		{"Foo.java", "java"},
		{"foo.cpp", "cpp"},
		{"foo.h", "c"}, // ambiguous; we treat .h as C
		{"Foo.cs", "csharp"},
		{"Gemfile.rb", "ruby"},
		{"README.md", "markdown"},
		{"chart.yaml", "yaml"},
		{"Makefile.local", ""}, // doesn't match "Makefile" exact
	}
	for _, tc := range cases {
		got := DetectFromPath(tc.path)
		if tc.want == "" {
			if got != nil {
				t.Errorf("DetectFromPath(%q) = %s, want nil", tc.path, got.Tag)
			}
			continue
		}
		if got == nil {
			t.Errorf("DetectFromPath(%q) = nil, want %s", tc.path, tc.want)
			continue
		}
		if string(got.Tag) != tc.want {
			t.Errorf("DetectFromPath(%q) = %s, want %s", tc.path, got.Tag, tc.want)
		}
	}
}

func TestDetectFromPath_BySpecialFilename(t *testing.T) {
	cases := []struct{ path, want string }{
		{"Dockerfile", "dockerfile"},
		{"dockerfile", "dockerfile"},
		{"Dockerfile.dev", "dockerfile"},
		{"Makefile", "makefile"},
		{"GNUmakefile", "makefile"},
		{"makefile", "makefile"},
	}
	for _, tc := range cases {
		got := DetectFromPath(tc.path)
		if got == nil {
			t.Errorf("DetectFromPath(%q) returned nil", tc.path)
			continue
		}
		if string(got.Tag) != tc.want {
			t.Errorf("DetectFromPath(%q) = %s, want %s", tc.path, got.Tag, tc.want)
		}
	}
}

func TestLookup(t *testing.T) {
	if Lookup("go") == nil {
		t.Error("Lookup(\"go\") == nil")
	}
	if Lookup("not-a-real-lang") != nil {
		t.Error("Lookup of unknown tag returned non-nil")
	}
}

func TestIsPassthrough(t *testing.T) {
	if IsPassthrough("python") {
		t.Error("python should not be passthrough")
	}
	if !IsPassthrough("yaml") {
		t.Error("yaml should be passthrough")
	}
}

func TestGrammarTag_DefaultsToLanguageTag(t *testing.T) {
	if got := Lookup("go").GrammarTag; got != "go" {
		t.Errorf("go grammar tag = %q, want %q", got, "go")
	}
	// C shares the cpp grammar, so its GrammarTag is explicitly "cpp".
	if got := Lookup("c").GrammarTag; got != "cpp" {
		t.Errorf("c grammar tag = %q, want %q (shares C++ grammar)", got, "cpp")
	}
}
