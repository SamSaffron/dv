package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var cleanupCmd = &cobra.Command{
	Use:        "remove",
	Aliases:    []string{"cleanup"},
	Short:      "Remove container and optionally its image",
	Deprecated: "use 'dv remove' instead of 'dv cleanup'",
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

		// If we removed the selected agent, choose the first remaining container for the selected image
		if cfg.SelectedAgent == name {
			// Determine image to filter by: prefer the container's recorded image, else the currently selected image
			imgName := cfg.ContainerImages[name]
			_, imgCfg, err := resolveImage(cfg, imgName)
			if err != nil {
				// Fallback to selected image silently
				_, imgCfg, _ = resolveImage(cfg, "")
			}

			out, _ := runShell("docker ps -a --format '{{.Names}}\t{{.Image}}'")
			var first string
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				parts := strings.SplitN(line, "\t", 2)
				if len(parts) < 2 {
					continue
				}
				n, image := parts[0], parts[1]
				if image != imgCfg.Tag {
					continue
				}
				first = n
				break
			}
			cfg.SelectedAgent = first
			_ = config.Save(configDir, cfg)
			if first != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Selected agent: %s\n", first)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Selected agent: (none)")
			}
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Cleanup complete")
		return nil
	},
}

func init() {
	cleanupCmd.Flags().Bool("image", false, "Also remove the Docker image after removing container")
	cleanupCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
}
