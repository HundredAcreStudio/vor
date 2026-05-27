package templates_test

import (
	"strings"
	"testing"

	gctx "github.com/repowise-dev/repowise-go/internal/generation/context"
	"github.com/repowise-dev/repowise-go/internal/generation/templates"
	"github.com/repowise-dev/repowise-go/internal/providers"
)

func TestFileOverviewRequest_IncludesPathAndSource(t *testing.T) {
	bundle := gctx.FileBundle{
		RelPath:  "internal/foo/bar.go",
		Language: "Go",
		Content:  "package foo\nfunc Bar() {}\n",
	}
	req := templates.FileOverviewRequest(bundle, "claude-opus-4-7")
	if req.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q", req.Model)
	}
	if req.Operation != "file_overview" {
		t.Errorf("Operation = %q", req.Operation)
	}
	if req.FilePath != "internal/foo/bar.go" {
		t.Errorf("FilePath = %q", req.FilePath)
	}
	if req.System == "" {
		t.Errorf("System prompt empty")
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != providers.RoleUser {
		t.Errorf("first message Role = %q", req.Messages[0].Role)
	}
	if !req.Messages[0].CacheControl {
		t.Errorf("expected CacheControl=true on source-bearing message")
	}
	if !strings.Contains(req.Messages[0].Content, "internal/foo/bar.go") {
		t.Errorf("user content missing file path")
	}
	if !strings.Contains(req.Messages[0].Content, "package foo") {
		t.Errorf("user content missing source body")
	}
}

func TestFileOverviewRequest_SurfacesSignals(t *testing.T) {
	bundle := gctx.FileBundle{
		RelPath:  "x.go",
		Language: "Go",
		Content:  "x",
		Signals: gctx.Signals{
			IsHotspot:       true,
			CommitCount90d:  42,
			PrimaryOwner:    "Alice <a@example>",
			DeadCodeReason:  "no callers and no entrypoint",
			HealthBiomarker: "god_object",
		},
	}
	req := templates.FileOverviewRequest(bundle, "")
	body := req.Messages[0].Content
	for _, want := range []string{"hotspot", "42 commits", "Alice", "dead-code", "god_object"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
}

func TestFileOverviewRequest_NoSignalsNoSection(t *testing.T) {
	bundle := gctx.FileBundle{RelPath: "x.go", Language: "Go", Content: "x"}
	req := templates.FileOverviewRequest(bundle, "")
	body := req.Messages[0].Content
	if strings.Contains(body, "Signals:") {
		t.Errorf("Signals section should be omitted when no signals present")
	}
}

func TestFileOverviewRequest_TruncatesLargeSource(t *testing.T) {
	big := strings.Repeat("x\n", templates.MaxPromptSourceBytes)
	bundle := gctx.FileBundle{RelPath: "x.go", Language: "Go", Content: big}
	req := templates.FileOverviewRequest(bundle, "")
	body := req.Messages[0].Content
	if !strings.Contains(body, "[truncated for prompt]") {
		t.Errorf("expected truncation marker for oversized source")
	}
	if len(body) > 2*templates.MaxPromptSourceBytes {
		t.Errorf("body length %d > 2× max", len(body))
	}
}
