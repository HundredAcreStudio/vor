package templates

import (
	"fmt"
	"strings"

	gctx "github.com/repowise-dev/repowise-go/internal/generation/context"
	"github.com/repowise-dev/repowise-go/internal/providers"
)

// MaxSymbolSourceBytes caps a symbol body before head/tail truncation.
// Smaller than the file-level cap because individual functions rarely
// exceed a few hundred lines; if they do, head+tail still beats raw
// truncation.
const MaxSymbolSourceBytes = 20_000

// symbolDetailSystem is the system prompt for per-symbol pages.
const symbolDetailSystem = `You are documenting one symbol (function, method, class, …) from a code repository.

Write a focused explainer: what this symbol does, who calls it, what it
calls, and any non-obvious behaviour that a maintainer would want to
know. Stay grounded — only document behaviour that's visible in the
source span or the caller/callee list. If purpose is unclear, write
"purpose unclear" rather than guessing.

Output exactly these markdown sections:

# Title (one line: "Symbol — short role")
## Signature (one line of pseudo-signature when language conventions
   make this useful; skip otherwise)
## Behaviour (two to four sentences)
## Inputs (bulleted, only when parameter list is non-trivial)
## Outputs (bulleted, only when return value is non-trivial)
## Callers / Callees (only when the lists materially inform usage —
   "called by X to do Y")
## Caveats (optional — non-obvious side effects, ordering constraints,
   error semantics)

Do not paste the full source body. A short quoted line is fine if it
clarifies the point.`

// SymbolDetailRequest builds the providers.Request for a symbol page.
func SymbolDetailRequest(bundle gctx.SymbolBundle, model string) providers.Request {
	src := gctx.TruncateForPrompt(bundle.SourceSpan, MaxSymbolSourceBytes)

	var b strings.Builder
	fmt.Fprintf(&b, "Symbol: %s\n", bundle.QualifiedName)
	fmt.Fprintf(&b, "Kind: %s\n", bundle.Kind)
	fmt.Fprintf(&b, "File: %s (lines %d–%d)\n", bundle.FilePath, bundle.StartLine, bundle.EndLine)
	if bundle.Language != "" {
		fmt.Fprintf(&b, "Language: %s\n", bundle.Language)
	}
	if len(bundle.Callers) > 0 {
		fmt.Fprintf(&b, "Callers: %s\n", strings.Join(bundle.Callers, ", "))
	}
	if len(bundle.Callees) > 0 {
		fmt.Fprintf(&b, "Callees: %s\n", strings.Join(bundle.Callees, ", "))
	}

	b.WriteString("\n--- Source span ---\n")
	b.WriteString(src)
	b.WriteString("\n--- End source ---\n\n")
	b.WriteString("Write the symbol detail as instructed.\n")

	return providers.Request{
		Model:     model,
		System:    symbolDetailSystem,
		Messages:  []providers.Message{{Role: providers.RoleUser, Content: b.String(), CacheControl: true}},
		MaxTokens: 1200,
		Operation: "symbol_detail",
		FilePath:  bundle.FilePath,
	}
}
