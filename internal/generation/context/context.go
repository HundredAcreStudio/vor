// Package context assembles the input bundle a generator needs to write
// one wiki page: the target file/symbol's text, the surrounding signals
// (graph neighbors, git hotspot status, dead-code findings, health
// biomarkers), and bookkeeping like the source hash.
//
// The bundle is provider-agnostic — templates render it into prompts,
// pages.Generator hands it to whichever Provider is configured.
package context

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// FileBundle is the input to a PageKindFileOverview generation.
type FileBundle struct {
	// RepoRoot is the absolute path to the repository root.
	RepoRoot string

	// RelPath is the repo-relative path of the target file (e.g.
	// "internal/foo/bar.go").
	RelPath string

	// Language is the detected source language (Go, Python, ...).
	Language string

	// Content is the raw text of the file.
	Content string

	// SourceHash is the SHA-256 of Content, used to detect staleness on
	// subsequent runs. Pre-computed so callers don't redo work.
	SourceHash string

	// Symbols lists the top-level symbol names exported by the file (from
	// the parser). Helps the prompt focus the summary.
	Symbols []string

	// Imports lists the file's import paths (raw, language-specific
	// strings).
	Imports []string

	// Neighbors are the qualified paths the file's edges point at (graph
	// outbound) and the ones that point at this file (graph inbound).
	// Helps the prompt say "this is called by X, Y, Z".
	NeighborsIn  []string
	NeighborsOut []string

	// Signals carries the cross-cutting analysis verdicts so prompts can
	// surface "this file is a hotspot" / "this file has a dead-code
	// finding" without an extra DB hit.
	Signals Signals
}

// Signals is the subset of analysis output relevant to a single file.
// Each field is a short, ready-to-render annotation.
type Signals struct {
	IsHotspot       bool
	IsStable        bool
	CommitCount90d  int
	PrimaryOwner    string // "Name <email>"
	DeadCodeReason  string // empty if not dead
	HealthBiomarker string // first health finding's biomarker name, if any
}

// LoadFile reads the file at filepath.Join(repoRoot, relPath), computes
// its source hash, and returns a minimally-populated FileBundle. Callers
// fill Symbols/Imports/Neighbors/Signals from their existing pipeline
// state.
func LoadFile(repoRoot, relPath, language string) (FileBundle, error) {
	abs := filepath.Join(repoRoot, relPath)
	data, err := os.ReadFile(abs)
	if err != nil {
		return FileBundle{}, err
	}
	content := string(data)
	return FileBundle{
		RepoRoot:   repoRoot,
		RelPath:    filepath.ToSlash(relPath),
		Language:   language,
		Content:    content,
		SourceHash: HashSource(content),
	}, nil
}

// HashSource computes the source hash used to detect staleness. Exported
// so callers that already have the content can reuse it without a second
// read.
func HashSource(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// TruncateForPrompt clips overly large content so the prompt stays within
// the model's context window. Strategy: keep the head and the tail,
// dropping the middle with a marker — better than a flat head-truncation
// because file footers (imports, exports, last func) often carry signal.
func TruncateForPrompt(content string, maxBytes int) string {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}
	const marker = "\n\n# … [truncated for prompt] …\n\n"
	headBytes := maxBytes / 2
	tailBytes := maxBytes - headBytes - len(marker)
	if tailBytes < 0 {
		return content[:maxBytes]
	}
	head := content[:headBytes]
	tail := content[len(content)-tailBytes:]
	// Cut at line boundaries when possible so we don't drop mid-statement.
	if i := strings.LastIndexByte(head, '\n'); i > 0 {
		head = head[:i]
	}
	if i := strings.IndexByte(tail, '\n'); i > 0 {
		tail = tail[i+1:]
	}
	return head + marker + tail
}
