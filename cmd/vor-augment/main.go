// vor-augment is the Claude Code Grep/Glob enrichment hook. It receives
// a JSON hook payload on stdin and emits an augmented payload on stdout.
//
// The Python implementation lives at packages/cli/src/vor/cli/augment_hook.py.
// Full behaviour is wired up in Phase 9; for now this is a stub that pass-throughs
// stdin to stdout so the binary can be installed during development.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "vor-augment: copy stdin->stdout:", err)
		os.Exit(1)
	}
}
