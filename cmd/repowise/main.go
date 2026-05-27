// repowise is the main CLI for repowise-go. Subcommands are registered in
// internal/cli/commands; this binary is a thin entrypoint.
package main

import (
	"fmt"
	"os"

	"github.com/repowise-dev/repowise-go/internal/cli/commands"
)

func main() {
	if err := commands.Root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
