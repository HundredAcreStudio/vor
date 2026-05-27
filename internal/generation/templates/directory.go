package templates

import (
	"fmt"
	"strings"

	gctx "github.com/repowise-dev/repowise-go/internal/generation/context"
	"github.com/repowise-dev/repowise-go/internal/providers"
)

// directoryOverviewSystem is the system prompt for per-directory pages.
// Different from file_overview: directories are about composition —
// "what does this folder collectively do?" — not implementation detail.
const directoryOverviewSystem = `You are documenting a directory in a code repository.

Write a concise overview that answers: what is this folder's purpose,
what are its main components, and how do they relate to each other.
Don't restate file-level details that already live in per-file pages
— summarise above that level. If the directory's purpose is unclear
from the file inventory, write "purpose unclear" rather than guessing.

Output exactly these markdown sections:

# Title (one line — name the folder's role, not its path)
## Purpose (two to three sentences)
## Components (bulleted, "name — one-line role"; one entry per file or
   subdirectory that materially shapes the directory's responsibility)
## Notable patterns (optional — design idioms shared across the
   contents, e.g. "every X package self-registers in init()")

Do not include code blocks.`

// DirectoryOverviewRequest builds the providers.Request for a directory
// page. The user message lists children + their existing summaries (if
// any) so the model can synthesise above file-level detail without
// having to read each file.
func DirectoryOverviewRequest(bundle gctx.DirectoryBundle, model string) providers.Request {
	var b strings.Builder
	display := bundle.RelPath
	if display == "" {
		display = "(repo root)"
	}
	fmt.Fprintf(&b, "Directory: %s\n", display)
	if bundle.DominantLanguage != "" {
		fmt.Fprintf(&b, "Dominant language: %s\n", bundle.DominantLanguage)
	}
	fmt.Fprintf(&b, "Contents: %d files, %d subdirectories\n\n",
		len(bundle.Children), len(bundle.Subdirectories))

	if len(bundle.Subdirectories) > 0 {
		b.WriteString("Subdirectories:\n")
		for _, d := range bundle.Subdirectories {
			fmt.Fprintf(&b, "- %s\n", d)
		}
		b.WriteByte('\n')
	}

	if len(bundle.Children) > 0 {
		b.WriteString("Files (with per-file summaries when available):\n")
		for _, c := range bundle.Children {
			summary := strings.TrimSpace(bundle.FileSummaries[c])
			if summary == "" {
				fmt.Fprintf(&b, "- %s\n", c)
			} else {
				fmt.Fprintf(&b, "- %s — %s\n", c, summary)
			}
		}
		b.WriteByte('\n')
	}

	b.WriteString("Write the directory overview as instructed.\n")

	return providers.Request{
		Model:     model,
		System:    directoryOverviewSystem,
		Messages:  []providers.Message{{Role: providers.RoleUser, Content: b.String(), CacheControl: true}},
		MaxTokens: 1200,
		Operation: "directory_overview",
		FilePath:  bundle.RelPath,
	}
}
