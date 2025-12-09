package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var copyCmd = &cobra.Command{
	Use:     "copy SOURCE CONTAINER_PATH",
	Aliases: []string{"cp"},
	Short:   "Copy a file or directory from host into the container",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		srcOnHost := args[0]
		dstInContainer := args[1]

		// Validate source exists on host
		if _, err := os.Stat(srcOnHost); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("source path does not exist: %s", srcOnHost)
			}
			return fmt.Errorf("failed to stat source path: %w", err)
		}

		// Resolve config and container
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		name, _ := cmd.Flags().GetString("name")
		if name == "" {
			name = currentAgentName(cfg)
		}

		if !docker.Running(name) {
			return fmt.Errorf("container '%s' is not running; run 'dv start' first", name)
		}

		// Copy with recursive ownership set to discourse:discourse
		if err := docker.CopyToContainerWithOwnership(name, srcOnHost, dstInContainer, true); err != nil {
			return fmt.Errorf("failed to copy %s to container %s:%s: %w", srcOnHost, name, dstInContainer, err)
		}

		fmt.Printf("Copied %s to %s:%s\n", srcOnHost, name, dstInContainer)
		return nil
	},
}

func init() {
	copyCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
}
