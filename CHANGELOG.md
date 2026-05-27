# Changelog

All notable changes to this project will be documented in this file.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- ADR + CHANGELOG decision extractors (Pass B)
- inline-marker decision extractor (Pass A)
- `repowise decisions` CLI subcommand
- `GET /api/repos/{id}/decisions` HTTP endpoint
- `repowise_decisions` MCP tool

### Changed
- **BREAKING**: per-language graph resolution moved to a registry — embedders that called the graph builder directly must now blank-import the resolver packages they need

### Removed
- BREAKING removal of the inline `if lang == "go"` resolver path inside graph.Builder
