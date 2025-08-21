package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "dv",
	Short:         "Discourse Vibe: manage local Discourse dev containers",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() error {
	return rootCmd.Execute()
}

func addPersistentFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose output")
}

func init() {
	addPersistentFlags(rootCmd)

	rootCmd.AddCommand(buildCmd)
	// run command is deprecated in favor of start + enter
	// rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(enterCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(cleanupCmd)
	// Top-level agent management commands
	rootCmd.AddCommand(agentListTopCmd)
	rootCmd.AddCommand(agentNewTopCmd)
	rootCmd.AddCommand(agentSelectTopCmd)
	rootCmd.AddCommand(agentRenameTopCmd)
	rootCmd.AddCommand(extractCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(dataCmd)
	rootCmd.AddCommand(imageCmd)
}

func exitIfErr(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
