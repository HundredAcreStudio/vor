package templates

import (
	"fmt"
	"strings"

	gctx "github.com/HundredAcreStudio/vor/internal/generation/context"
	"github.com/HundredAcreStudio/vor/internal/providers"
)

// architectureSystem is the system prompt for the repo-wide overview page.
// It's the top of the wiki — a newcomer's orientation — so it synthesises
// above module/file level and grounds everything in the supplied facts.
const architectureSystem = `You are writing the top-level architecture overview for a software repository — the first page a new engineer reads.

Synthesise from the supplied facts only; do not invent components, files, or technologies. Stay above file-level detail (per-file pages cover that). If a section has no supporting data, omit it.

Output these markdown sections:

# Repository Overview: <name>
## Project Summary (2–4 sentences: what the repository is and what it does)
## Technology Stack (bulleted, from the language breakdown — name the primary languages and their share, plus notable tooling if evident)
## Architecture (a few sentences on how the major modules fit together; if circular-dependency clusters are reported, call them out as a risk)
## Key Modules (bulleted, "module — its role")
## Entry Points (bulleted — where to start reading, from the listed files)
## Notable Decisions (optional — only if decisions are provided)

Do not include code blocks.`

// ArchitectureRequest builds the providers.Request for the repo overview page
// from the assembled architecture facts.
func ArchitectureRequest(b gctx.ArchitectureBundle, model string) providers.Request {
	var s strings.Builder
	fmt.Fprintf(&s, "Repository: %s\n", b.RepoName)
	fmt.Fprintf(&s, "Indexed: %d files, %d symbols", b.FileCount, b.SymbolCount)
	if b.HealthAvg > 0 {
		fmt.Fprintf(&s, "; average health score %.1f/10", b.HealthAvg)
	}
	s.WriteString("\n\n")

	if len(b.Languages) > 0 {
		s.WriteString("Language breakdown (by files):\n")
		for _, l := range b.Languages {
			fmt.Fprintf(&s, "- %s — %d files (%.0f%%)\n", l.Language, l.Files, l.Pct)
		}
		s.WriteByte('\n')
	}

	if len(b.Modules) > 0 {
		s.WriteString("Top modules (by size):\n")
		for _, m := range b.Modules {
			fmt.Fprintf(&s, "- %s — %d files, %d symbols\n", m.Name, m.Files, m.Symbols)
		}
		s.WriteByte('\n')
	}

	if len(b.EntryPoints) > 0 {
		s.WriteString("Entry points (highest-ranked, read first):\n")
		for _, e := range b.EntryPoints {
			fmt.Fprintf(&s, "- %s\n", e)
		}
		s.WriteByte('\n')
	}

	if b.CommunityCount > 0 {
		fmt.Fprintf(&s, "Circular-dependency clusters detected: %d\n", b.CommunityCount)
		for _, c := range b.TopCommunities {
			fmt.Fprintf(&s, "- %s\n", c)
		}
		s.WriteByte('\n')
	}

	if b.DecisionCount > 0 {
		fmt.Fprintf(&s, "Recorded architectural decisions: %d\n", b.DecisionCount)
		for _, d := range b.TopDecisions {
			fmt.Fprintf(&s, "- %s\n", d)
		}
		s.WriteByte('\n')
	}

	s.WriteString("Write the repository overview as instructed.\n")

	return providers.Request{
		Model:     model,
		System:    architectureSystem,
		Messages:  []providers.Message{{Role: providers.RoleUser, Content: s.String(), CacheControl: true}},
		MaxTokens: 1500,
		Operation: "architecture",
	}
}
