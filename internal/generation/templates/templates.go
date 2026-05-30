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

// fileOverviewSystem is the static system prompt for per-file pages. This page
// is the single, comprehensive entry for a file — symbols are documented inline
// here rather than as separate pages.
const fileOverviewSystem = `You are a senior software engineer writing the wiki page for a single source file. This page is the ONE place a developer learns what this file does and how to use it — be comprehensive but strictly grounded. Use only what the supplied source, symbols, imports, and signals show; never invent APIs, parameters, or behavior. When something is genuinely unclear, say so rather than guessing.

Output these markdown sections, in order. Omit an OPTIONAL section when there is nothing grounded to say.

# Title (one line, sentence case — the file's role)

## Overview
Two to four sentences: what this file is, what it does, and where it sits in the system.

(Hotspot/health callout — only if the Signals say this file is a hotspot and/or carries a high-complexity or other health biomarker: immediately after the Overview, add a paragraph beginning "**Important:**" that states the risk — high change frequency and/or complexity — and what to be careful about when modifying it. Omit entirely when no such signal is present.)

## Public API
Document the public/exported symbols. For each, lead with its name (bold or as a sub-heading), say what it does, and — for functions/methods — list parameters as "name (type) — meaning" and the return value/effects; for a class, give its responsibility and key methods. Derive signatures and parameters from the source. Skip trivial private helpers.

## Dependencies (optional)
From the imports, the notable modules/packages this file depends on and what each is used for; group related ones.

## Usage Notes (optional)
Concrete usage evident from the code — how it's invoked, flags/options, short examples.

## Troubleshooting (optional)
Footguns, best-effort/try-except behavior, edge cases, or gotchas visible in the code.

Use code blocks sparingly — short snippets only when they materially help. Do not restate the path/language metadata already shown to the user.`

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
		MaxTokens: 2800, // richer single-entry page (overview, API, deps, usage)
		Operation: "file_overview",
		FilePath:  bundle.RelPath,
	}
}

func signalsPresent(s gctx.Signals) bool {
	return s.IsHotspot || s.IsStable || s.PrimaryOwner != "" ||
		s.DeadCodeReason != "" || s.HealthBiomarker != ""
}
