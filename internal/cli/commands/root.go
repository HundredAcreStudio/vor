// Package commands wires up the cobra command tree for the repowise CLI.
// Each subcommand lives in its own file so the root file stays small.
package commands

import (
	"github.com/spf13/cobra"
)

// Root returns the top-level cobra command. It is exposed as a function so
// tests and the cmd/repowise main can each obtain a fresh tree.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "repowise",
		Short:         "Codebase intelligence layer for AI coding agents",
		Long:          longDescription,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newVersionCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newDBCmd())
	root.AddCommand(newIngestCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newHealthCmd())
	root.AddCommand(newDeadCodeCmd())
	root.AddCommand(newDecisionsCmd())
	root.AddCommand(newGenerateCmd())
	root.AddCommand(newPagesCmd())
	root.AddCommand(newUpdateCmd())
	root.AddCommand(newReindexCmd())
	root.AddCommand(newDeleteCmd())
	root.AddCommand(newClaudeMdCmd())
	root.AddCommand(newHotspotsCmd())
	root.AddCommand(newSearchCmd())
	root.AddCommand(newExternalsCmd())
	root.AddCommand(newCostsCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newPipelineCmd())
	root.AddCommand(newMCPCmd())
	root.AddCommand(newCompletionCmd())
	// Additional subcommands are added in their respective phases.

	return root
}

const longDescription = `repowise indexes a codebase into five intelligence layers — dependency graph,
git history, auto-generated documentation, architectural decisions, and code
health — and exposes them over MCP and HTTP so AI coding agents can answer
questions without re-reading the source every time.

This is the Go port. See PORTING_PLAN.md for status and roadmap.`
