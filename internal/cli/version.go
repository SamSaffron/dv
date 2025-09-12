package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// These will be set by the linker during build
var version = "dev"
var commit = "unknown"
var date = "unknown"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("dv version %s (commit: %s, built: %s)\n", version, commit, date)
	},
}
