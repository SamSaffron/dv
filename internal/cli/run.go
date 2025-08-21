package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Deprecated: run has been superseded by start (to create or start) and enter (to open a shell or run commands).
var runCmd = &cobra.Command{
	Use:        "run",
	Short:      "DEPRECATED: use 'start' to start and 'enter' to open a shell",
	Deprecated: "use 'dv start' to start/create and 'dv enter' to open a shell",
	Args:       cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "'dv run' is deprecated. Use 'dv start' to create/start the container, and 'dv enter' to open a shell or run a command.")
		return nil
	},
}

func init() {}

// helper moved to shared.go
