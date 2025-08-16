package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the container",
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil { return err }
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil { return err }

		name, _ := cmd.Flags().GetString("name")
		if name == "" { name = currentAgentName(cfg) }

		if !docker.Exists(name) {
			fmt.Fprintf(cmd.OutOrStdout(), "Container '%s' does not exist\n", name)
			return nil
		}
		if !docker.Running(name) {
			fmt.Fprintf(cmd.OutOrStdout(), "Container '%s' is already stopped\n", name)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Stopping container '%s'...\n", name)
		return docker.Stop(name)
	},
}

func init() {
	stopCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
}
