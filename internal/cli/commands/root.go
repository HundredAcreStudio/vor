// Package commands wires up the cobra command tree for the vor CLI.
// Each subcommand lives in its own file so the root file stays small.
package commands

import (
	"github.com/spf13/cobra"
)

// Root returns the top-level cobra command. It is exposed as a function so
// tests and the cmd/vor main can each obtain a fresh tree.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "vor",
		Short:         "Codebase intelligence layer for AI coding agents",
		Long:          longDescription,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// The CLI is intentionally lean: bootstrap/ops + repo lifecycle +
	// scriptable read shortcuts. Browsing (health, hotspots, decisions,
	// pages, security, costs, coverage, pipeline) and generation (generate,
	// embed, export) now live in the dashboard; freshness is handled by the
	// daemon's auto-indexer, which retired the `hook`/`update`/`ingest`
	// commands.
	root.AddCommand(newVersionCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newMCPCmd())
	root.AddCommand(newDaemonCmd())

	root.AddCommand(newRegisterCmd())
	root.AddCommand(newUnregisterCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newReindexCmd())
	root.AddCommand(newDeleteCmd())

	root.AddCommand(newStatusCmd())
	root.AddCommand(newSearchCmd())
	root.AddCommand(newClaudeMdCmd())

	root.AddCommand(newDBCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newCompletionCmd())

	return root
}

const longDescription = `vor indexes a codebase into five intelligence layers — dependency graph,
git history, auto-generated documentation, architectural decisions, and code
health — and exposes them over MCP and HTTP so AI coding agents can answer
questions without re-reading the source every time.

This is the Go port. See PORTING_PLAN.md for status and roadmap.`
