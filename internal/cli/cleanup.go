package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Remove container and optionally the image",
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		removeImage, _ := cmd.Flags().GetBool("all")
		name, _ := cmd.Flags().GetString("name")
		if name == "" {
			name = currentAgentName(cfg)
		}

		if docker.Exists(name) {
			fmt.Fprintf(cmd.OutOrStdout(), "Stopping and removing container '%s'...\n", name)
			_ = docker.Stop(name)
			_ = docker.Remove(name)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Container '%s' does not exist\n", name)
		}

		if removeImage {
			if docker.ImageExists(cfg.ImageTag) {
				fmt.Fprintf(cmd.OutOrStdout(), "Removing image '%s'...\n", cfg.ImageTag)
				_ = docker.RemoveImage(cfg.ImageTag)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Image '%s' does not exist\n", cfg.ImageTag)
			}
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Cleanup complete")
		return nil
	},
}

func init() {
	cleanupCmd.Flags().Bool("all", false, "Also remove the Docker image after removing container")
	cleanupCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
}
