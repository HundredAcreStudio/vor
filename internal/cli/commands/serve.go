package commands

import (
	"errors"

	"github.com/spf13/cobra"
)

// newServeCmd registers the `repowise serve` subcommand. The HTTP + MCP
// server itself lands in Phase 8; this stub claims the command surface so
// `--help` output is stable and CI invocations don't break.
func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP + MCP server (not implemented yet)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("serve is not implemented yet — pending Phase 8")
		},
	}
}
