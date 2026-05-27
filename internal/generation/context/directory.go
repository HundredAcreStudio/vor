package context

import (
	"sort"
	"strings"
)

// DirectoryBundle is the input to a PageKindDirectoryOverview generation.
//
// A directory page summarises a folder's purpose, the modules it
// contains, and how it fits into the broader repo. The bundle therefore
// carries the directory path itself plus condensed signals from the
// directory's contents — not file source bodies (those go into per-file
// pages).
type DirectoryBundle struct {
	// RepoRoot is the absolute path to the repository root.
	RepoRoot string

	// RelPath is the repo-relative directory path (e.g.
	// "internal/generation/pages"). The empty string means "repo root".
	RelPath string

	// Children are direct child file paths within this directory.
	Children []string

	// Subdirectories are direct child directories of RelPath (one level
	// deep). Useful for pointing the model at "and there's a templates
	// subfolder over there".
	Subdirectories []string

	// FileSummaries pairs each child file path with its existing
	// file_overview summary (if any). Lets the directory model write a
	// higher-level summary without re-reading every file. Empty when no
	// file pages exist yet — the prompt degrades gracefully.
	FileSummaries map[string]string

	// SourceHash is computed across the directory's child file paths and
	// (when available) their summaries. Drives staleness detection.
	SourceHash string

	// DominantLanguage is the most-common language among Children, used
	// in the prompt header.
	DominantLanguage string
}

// NewDirectoryBundle constructs a DirectoryBundle from caller-supplied
// inputs and computes a deterministic SourceHash from them. Callers
// (typically the runner) provide the child + summary data from the
// indexed state.
func NewDirectoryBundle(repoRoot, relPath string, children, subdirs []string, summaries map[string]string, dominantLang string) DirectoryBundle {
	sortedChildren := append([]string(nil), children...)
	sort.Strings(sortedChildren)
	sortedSubdirs := append([]string(nil), subdirs...)
	sort.Strings(sortedSubdirs)

	var sb strings.Builder
	sb.WriteString(relPath)
	sb.WriteByte('\n')
	for _, c := range sortedChildren {
		sb.WriteString(c)
		sb.WriteByte('\t')
		if summary, ok := summaries[c]; ok {
			sb.WriteString(summary)
		}
		sb.WriteByte('\n')
	}
	for _, d := range sortedSubdirs {
		sb.WriteString(d)
		sb.WriteByte('\n')
	}

	return DirectoryBundle{
		RepoRoot:         repoRoot,
		RelPath:          relPath,
		Children:         sortedChildren,
		Subdirectories:   sortedSubdirs,
		FileSummaries:    summaries,
		SourceHash:       HashSource(sb.String()),
		DominantLanguage: dominantLang,
	}
}
