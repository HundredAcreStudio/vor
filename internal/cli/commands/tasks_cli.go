package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/pipeline/tasks"
)

// reportTaskOutcomes prints a one-line summary per post-pipeline task that
// ran. Silent when no tasks ran (none enabled / none registered).
func reportTaskOutcomes(cmd *cobra.Command, outcomes []tasks.Outcome) {
	out := cmd.OutOrStdout()
	for _, o := range outcomes {
		switch {
		case o.Err != nil:
			fmt.Fprintf(cmd.ErrOrStderr(), "task %s failed: %v\n", o.TaskID, o.Err)
		case o.Result.Skipped:
			fmt.Fprintf(out, "task %s skipped: %s\n", o.TaskID, o.Result.Detail)
		default:
			fmt.Fprintf(out, "task %s: %s\n", o.TaskID, o.Result.Detail)
		}
	}
}
