package context_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gctx "github.com/repowise-dev/repowise-go/internal/generation/context"
)

func TestLoadFile_PopulatesBundle(t *testing.T) {
	dir := t.TempDir()
	rel := "foo/bar.go"
	full := filepath.Join(dir, rel)
	_ = os.MkdirAll(filepath.Dir(full), 0o755)
	_ = os.WriteFile(full, []byte("package foo\n"), 0o644)

	b, err := gctx.LoadFile(dir, rel, "Go")
	if err != nil {
		t.Fatal(err)
	}
	if b.RelPath != "foo/bar.go" {
		t.Errorf("RelPath = %q", b.RelPath)
	}
	if b.Content != "package foo\n" {
		t.Errorf("Content = %q", b.Content)
	}
	if b.SourceHash == "" || len(b.SourceHash) != 64 {
		t.Errorf("SourceHash unexpected: %q", b.SourceHash)
	}
}

func TestHashSource_DeterministicAndDifferent(t *testing.T) {
	a := gctx.HashSource("hello")
	b := gctx.HashSource("hello")
	c := gctx.HashSource("hello!")
	if a != b {
		t.Errorf("same input → different hashes: %q vs %q", a, b)
	}
	if a == c {
		t.Errorf("different inputs → same hash")
	}
}

func TestTruncateForPrompt_KeepsHeadAndTail(t *testing.T) {
	src := strings.Repeat("HEAD\n", 100) + strings.Repeat("middle\n", 1000) + strings.Repeat("TAIL\n", 100)
	out := gctx.TruncateForPrompt(src, 200)
	if !strings.HasPrefix(out, "HEAD") {
		t.Errorf("output does not start with head: %q", out[:30])
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "TAIL") {
		t.Errorf("output does not end with tail: %q", out[len(out)-30:])
	}
	if !strings.Contains(out, "[truncated for prompt]") {
		t.Errorf("missing truncation marker")
	}
}

func TestTruncateForPrompt_SmallSourceUnchanged(t *testing.T) {
	src := "small"
	out := gctx.TruncateForPrompt(src, 1000)
	if out != src {
		t.Errorf("small source modified: %q", out)
	}
}

func TestTruncateForPrompt_ZeroMaxUnchanged(t *testing.T) {
	out := gctx.TruncateForPrompt("anything", 0)
	if out != "anything" {
		t.Errorf("max=0 should disable truncation")
	}
}
