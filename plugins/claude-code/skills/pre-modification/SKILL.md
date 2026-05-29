---
name: pre-modification-check
description: >
  Use before modifying, refactoring, renaming, or deleting code in a repository indexed by Vor
  (its CLAUDE.md has a Vor-managed section, or the `vor` MCP server is connected). Activates when
  about to edit code — especially shared utilities, core modules, or files the user didn't explicitly
  name — to assess blast radius and avoid breaking dependents or violating architectural decisions.
user-invocable: false
---

# Pre-Modification Check with Vor

Before changing files in a Vor-indexed repository, assess the impact with the
`vor` MCP tools instead of guessing at the blast radius.

## Before editing a file

Call `vor_risk(targets=["path/to/file.go"])`. It reports:

- **Hotspot / churn** — is this a high-churn file? Extra care needed.
- **Dependents (blast radius)** — how many files import this, and which ones. API changes here ripple to them.
- **Co-change partners** — files that historically change together with this one; you may need to update them too.
- **Ownership + bus factor** — who owns it; a bus factor of 1 means a single maintainer (route review accordingly).
- **Governing decisions** — architectural decisions that constrain this file; read them before diverging.
- **A derived risk level** (high / medium / low) with reasons.

Batch every file you intend to touch into one call:
`vor_risk(targets=["a.go", "b.go", "pkg/"])`.

## Warn the user when risk is high

Surface it explicitly before editing when `vor_risk` reports:

- **risk = "high"**, or a **hotspot** above the 90th churn percentile.
- **10+ dependents** — list the top dependents; an API change will break them.
- **bus factor 1** — note a single person maintains this code.
- **governing decisions present** — mention them; don't silently contradict an intentional design choice.

## Before refactoring, renaming, or moving code

First call `vor_get_why(targets=["file.go"])` (or `query="..."`) to learn the
decision behind the current shape, and `vor_get_context(targets=[...])` for the
full triage card. This prevents accidentally undoing a deliberate decision.

## Error handling

If a `vor_*` tool errors, the MCP server may not be running or the repo isn't
indexed. Proceed with the change, but note that risk assessment was unavailable
(suggest the user run `vor register .` / start the daemon).
