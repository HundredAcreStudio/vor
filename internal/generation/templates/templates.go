// Package templates turns a context bundle into the System + Messages
// fields of a providers.Request. One template per PageKind. Templates
// stay in Go strings (not external files) so the binary stays
// dependency-free and the prompt-vs-code diff is reviewable in one place.
//
// Templates aim for: short system prompt, explicit output shape, every
// signal the context bundle carries surfaced to the model. Bias is
// towards grounded summaries — discourage speculation, ask the model to
// say "unclear" when it doesn't know.
package templates

import (
	"fmt"
	"strings"

	gctx "github.com/HundredAcreStudio/vor/internal/generation/context"
	"github.com/HundredAcreStudio/vor/internal/providers"
)

// MaxPromptSourceBytes caps the file body passed to the model. Above
// this we head+tail-truncate via context.TruncateForPrompt. Picked to
// leave ~150K tokens of headroom on a Sonnet-class context window even
// with a verbose neighbor list. Tunable per-call via the public API.
const MaxPromptSourceBytes = 60_000

// fileOverviewSystem is the static system prompt for per-file pages.
const fileOverviewSystem = `You are a senior software engineer documenting a code repository.

Write a concise overview of the supplied source file. Your audience is
another engineer who has never seen this file. Stay grounded in what
the code actually does — do not speculate about features that aren't
visible, and if a section's purpose is unclear, write "unclear" rather
than inventing rationale.

Output exactly these markdown sections, in order:

# Title (one line, sentence case)
## Summary (two to four sentences)
## Responsibilities (bulleted, what this file owns)
## Key symbols (bulleted, name — one-line role)
## Notable signals (only if hotspot / dead-code / health markers present)
## Caveats (optional — surprising decisions, footguns, dependencies)

Do not include code blocks unless quoting one or two lines is essential.
Do not restate metadata (path, language) that's already shown to the user.`

// FileOverviewRequest builds a providers.Request that generates a
// PageKindFileOverview for the supplied bundle. Operation is set to
// "file_overview" so cost accounting can attribute spend.
func FileOverviewRequest(bundle gctx.FileBundle, model string) providers.Request {
	body := gctx.TruncateForPrompt(bundle.Content, MaxPromptSourceBytes)

	var b strings.Builder
	b.WriteString("File: ")
	b.WriteString(bundle.RelPath)
	b.WriteString("\nLanguage: ")
	b.WriteString(bundle.Language)
	b.WriteByte('\n')

	if len(bundle.Symbols) > 0 {
		fmt.Fprintf(&b, "Top-level symbols (from parser): %s\n",
			strings.Join(bundle.Symbols, ", "))
	}
	if len(bundle.Imports) > 0 {
		fmt.Fprintf(&b, "Imports: %s\n", strings.Join(bundle.Imports, ", "))
	}
	if len(bundle.NeighborsIn) > 0 {
		fmt.Fprintf(&b, "Called by: %s\n", strings.Join(bundle.NeighborsIn, ", "))
	}
	if len(bundle.NeighborsOut) > 0 {
		fmt.Fprintf(&b, "Calls into: %s\n", strings.Join(bundle.NeighborsOut, ", "))
	}

	if s := bundle.Signals; signalsPresent(s) {
		b.WriteString("\nSignals:\n")
		if s.IsHotspot {
			fmt.Fprintf(&b, "- hotspot (%d commits in last 90 days)\n", s.CommitCount90d)
		}
		if s.IsStable {
			b.WriteString("- stable (low churn)\n")
		}
		if s.PrimaryOwner != "" {
			fmt.Fprintf(&b, "- primary owner: %s\n", s.PrimaryOwner)
		}
		if s.DeadCodeReason != "" {
			fmt.Fprintf(&b, "- dead-code finding: %s\n", s.DeadCodeReason)
		}
		if s.HealthBiomarker != "" {
			fmt.Fprintf(&b, "- health biomarker: %s\n", s.HealthBiomarker)
		}
	}

	b.WriteString("\n--- Source ---\n")
	b.WriteString(body)
	b.WriteString("\n--- End source ---\n")
	b.WriteString("\nWrite the overview as instructed.")

	// Mark the source-bearing user message as cache-eligible: this is the
	// large prefix that benefits most when the same file is regenerated.
	msg := providers.Message{
		Role:         providers.RoleUser,
		Content:      b.String(),
		CacheControl: true,
	}

	return providers.Request{
		Model:     model,
		System:    fileOverviewSystem,
		Messages:  []providers.Message{msg},
		MaxTokens: 1500,
		Operation: "file_overview",
		FilePath:  bundle.RelPath,
	}
}

func signalsPresent(s gctx.Signals) bool {
	return s.IsHotspot || s.IsStable || s.PrimaryOwner != "" ||
		s.DeadCodeReason != "" || s.HealthBiomarker != ""
}
