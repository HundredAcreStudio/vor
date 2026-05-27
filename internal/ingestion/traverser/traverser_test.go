package traverser

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fixtureRoot resolves the path to testdata/sample-repo from the test's CWD.
// Go runs each test from the package dir, so we walk up to the repo root.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../../../testdata/sample-repo")
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}
	return abs
}

func collectPaths(t *testing.T, opts Options) []string {
	t.Helper()
	tr, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	files, _, err := tr.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	slices.Sort(out)
	return out
}

func TestTraverse_HappyPath(t *testing.T) {
	root := fixtureRoot(t)
	paths := collectPaths(t, Options{RepoRoot: root})

	// Must include: real code + docs + Dockerfile.
	want := []string{
		"Dockerfile",
		"README.md",
		"main.py",
		"pkg/foo.go",
		"src/bar.py",
		"src/test_bar.py",
	}
	for _, w := range want {
		if !slices.Contains(paths, w) {
			t.Errorf("expected %q in traversal, got %v", w, paths)
		}
	}
}

func TestTraverse_FiltersAreApplied(t *testing.T) {
	root := fixtureRoot(t)
	paths := collectPaths(t, Options{RepoRoot: root})

	// Must NOT include any of these.
	notWant := []string{
		"node_modules/lib/junk.js",   // blocked dir
		"build/built.js",             // .gitignore'd
		"bundle.min.js",              // blocked filename pattern
		"package-lock.json",          // blocked filename pattern
		"secret.txt",                 // .repowiseIgnore
		"ignored_by_repowise.py",     // .repowiseIgnore
		"ignored_by_gitignore.py",    // .gitignore
		"pkg/generated.go",           // generated header marker
		"pkg/binary.go",              // null byte → binary
	}
	for _, nw := range notWant {
		if slices.Contains(paths, nw) {
			t.Errorf("did not expect %q in traversal, paths = %v", nw, paths)
		}
	}
}

func TestTraverse_StatsCounters(t *testing.T) {
	root := fixtureRoot(t)
	tr, err := New(Options{RepoRoot: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, stats, err := tr.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if stats.Included == 0 {
		t.Errorf("Included = 0; expected > 0")
	}
	// At least one .gitignore skip (ignored_by_gitignore.py)
	if stats.SkippedGitignore == 0 {
		t.Errorf("SkippedGitignore = 0; expected > 0")
	}
	// At least one .repowiseIgnore skip
	if stats.SkippedExtraIgnore == 0 {
		t.Errorf("SkippedExtraIgnore = 0; expected > 0")
	}
	// generated.go should be counted under SkippedGenerated.
	if stats.SkippedGenerated == 0 {
		t.Errorf("SkippedGenerated = 0; expected > 0")
	}
	// binary.go should be counted under SkippedBinary.
	if stats.SkippedBinary == 0 {
		t.Errorf("SkippedBinary = 0; expected > 0")
	}
	// Language counts must mention go + python at least.
	if stats.LangCounts["go"] == 0 {
		t.Errorf("LangCounts[\"go\"] = 0")
	}
	if stats.LangCounts["python"] == 0 {
		t.Errorf("LangCounts[\"python\"] = 0")
	}
	if stats.LangCounts["dockerfile"] == 0 {
		t.Errorf("LangCounts[\"dockerfile\"] = 0; Dockerfile should be detected")
	}
}

func TestTraverse_TestFileDetection(t *testing.T) {
	root := fixtureRoot(t)
	tr, err := New(Options{RepoRoot: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	files, _, _ := tr.Collect(context.Background())
	var testBar bool
	for _, f := range files {
		if f.Path == "src/test_bar.py" {
			testBar = f.IsTest
		}
	}
	if !testBar {
		t.Errorf("src/test_bar.py should have IsTest=true")
	}
}

func TestTraverse_EntryPointDetection(t *testing.T) {
	root := fixtureRoot(t)
	tr, err := New(Options{RepoRoot: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	files, _, _ := tr.Collect(context.Background())
	var mainPy bool
	for _, f := range files {
		if f.Path == "main.py" {
			mainPy = f.IsEntryPoint
		}
	}
	if !mainPy {
		t.Errorf("main.py should have IsEntryPoint=true")
	}
}

func TestTraverse_OversizedFile(t *testing.T) {
	// Use the same fixture but cap MaxFileSizeKB low enough that the
	// package-lock.json fixture would be over the limit if it weren't
	// already filtered by pattern. To get a clean signal, point at a tmp
	// repo with one large file.
	tmp := t.TempDir()
	bigPath := filepath.Join(tmp, "big.go")
	big := strings.Repeat("// padding\n", 200) // ~2.2 KB
	if err := writeFile(bigPath, big); err != nil {
		t.Fatalf("write big file: %v", err)
	}
	if err := writeFile(filepath.Join(tmp, "small.go"), "package x\n"); err != nil {
		t.Fatalf("write small file: %v", err)
	}

	tr, err := New(Options{RepoRoot: tmp, MaxFileSizeKB: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	files, stats, err := tr.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(files) != 1 || files[0].Path != "small.go" {
		t.Errorf("expected only small.go to survive size cap, got %+v", files)
	}
	if stats.SkippedOversized != 1 {
		t.Errorf("SkippedOversized = %d, want 1", stats.SkippedOversized)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
