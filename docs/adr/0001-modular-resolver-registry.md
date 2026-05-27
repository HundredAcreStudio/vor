# Use modular resolver registry instead of inline language conditionals

## Status
Accepted

## Context
Early port of the graph builder embedded `if lang == "go" { ... }` blocks
that hard-coded import resolution. Adding a language meant editing the
builder, which scales badly across our 9-language target.

## Decision
Each language gets its own resolver subpackage under
`internal/ingestion/graph/resolver/<lang>`. Packages self-register in
`init()` so adding a language is "drop a file + add a side-effect import"
rather than "modify central code".

## Consequences
- Per-language testability — each resolver gets its own fixture project
- Easy to extend to Ruby / PHP / Swift / Kotlin later
- Slightly higher friction for cross-cutting refactors of the Resolver interface
