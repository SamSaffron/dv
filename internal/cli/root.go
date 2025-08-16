package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "dv",
	Short: "Discourse Vibe: manage local Discourse dev containers",
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
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(cleanupCmd)
	rootCmd.AddCommand(agentCmd)
	rootCmd.AddCommand(extractCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(dataCmd)
}

func exitIfErr(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
