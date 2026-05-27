# Use modernc.org/sqlite (pure-Go) instead of mattn/go-sqlite3 (cgo)

## Status
Accepted

## Context
Builds need to work without a C toolchain in CI containers and on
contributors' machines. cgo also complicates cross-compilation.

## Decision
Standardise on modernc.org/sqlite for the SQLite driver. The schema +
queries are vanilla enough that the perf delta vs the cgo driver
doesn't matter for this workload.

## Consequences
- No cgo dependency for SQLite (tree-sitter still requires it)
- ~30% slower on heavy write benchmarks (acceptable)
- One fewer toolchain dependency for new contributors
