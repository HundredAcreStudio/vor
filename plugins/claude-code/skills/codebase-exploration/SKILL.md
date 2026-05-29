---
name: codebase-exploration
description: >
  Use when exploring, understanding, or answering questions about a repository indexed by Vor
  (its CLAUDE.md has a Vor-managed section, or the `vor` MCP server is connected). Activates for
  "how does X work", "explain the architecture", "where is Y implemented", "what should I look at",
  or any task that needs codebase understanding before diving into source files.
user-invocable: false
---

# Codebase Exploration with Vor

This repository has a Vor intelligence layer. Before reading raw source to
orient, use the `vor` MCP tools — one call answers from the index instead of
grepping and reading dozens of files, and includes ownership, history, and the
decisions behind the code.

## When starting on an unfamiliar repo

1. `vor_status()` — counts, hotspots, health, dependency totals: what's been indexed.
2. `vor_get_architecture_diagram()` — the module map, inter-module dependencies, and entry points (pass `format="mermaid"` for a renderable diagram).
3. `vor_attention()` — the prioritized "what should I look at" digest: knowledge silos, churn hotspots, dead code, decisions awaiting review.

## When answering "how does X work" / "where is Y handled"

1. `vor_get_answer(question="...")` for a synthesized, cited answer, **or**
2. `vor_search(query="X")` to find relevant symbols/files, then
   `vor_get_context(targets=[...])` for documentation, ownership, and decisions on those targets (batch them in one call).
3. Only read raw source when the indexed answer isn't specific enough.

## When asked why the code is shaped a certain way

`vor_get_why(query="why ...")` or `vor_get_why(targets=["file"])` — returns the
architectural decisions (inline markers, ADRs, CHANGELOG, commit archaeology)
behind the design. Call this before proposing a different approach.

## When tracing connections or execution

- `vor_get_dependency_path(from="a", to="b")` — how one part reaches another.
- `vor_get_execution_flows(entry="...")` — what runs from an entry point.

## Error handling

If `vor_*` tools error or return nothing, the repo may not be indexed or the
server isn't running — suggest `vor register .` (or starting the daemon), then
fall back to reading source directly.
