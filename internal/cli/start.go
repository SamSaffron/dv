package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"dv/internal/assets"
	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var startCmd = &cobra.Command{
	Use:   "start [--reset] [--name NAME] [--image NAME] [--host-starting-port N] [--container-port N]",
	Short: "Create or start a container for the selected image",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		reset, _ := cmd.Flags().GetBool("reset")
		name, _ := cmd.Flags().GetString("name")
		imageOverride, _ := cmd.Flags().GetString("image")
		if name == "" {
			name = currentAgentName(cfg)
		}

		hostPort, _ := cmd.Flags().GetInt("host-starting-port")
		containerPort, _ := cmd.Flags().GetInt("container-port")
		if hostPort == 0 {
			hostPort = cfg.HostStartingPort
		}
		if containerPort == 0 {
			containerPort = cfg.ContainerPort
		}

		// Determine which image and workdir to use from image selection
		imgName, imgCfg, err := resolveImage(cfg, imageOverride)
		if err != nil {
			return err
		}
		imageTag := imgCfg.Tag
		workdir := imgCfg.Workdir
		isTheme := imgCfg.Kind == "theme"

		if reset && docker.Exists(name) {
			fmt.Fprintf(cmd.OutOrStdout(), "Stopping and removing existing container '%s'...\n", name)
			_ = docker.Stop(name)
			_ = docker.Remove(name)
		}

		if !docker.Exists(name) {
			// Find the first available host port, starting from hostPort
			chosenPort := hostPort
			for isPortInUse(chosenPort) {
				chosenPort++
			}
			if chosenPort != hostPort {
				fmt.Fprintf(cmd.OutOrStdout(), "Port %d in use, using %d.\n", hostPort, chosenPort)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Creating and starting container '%s' with image '%s'...\n", name, imageTag)
			if err := docker.RunDetached(name, workdir, imageTag, chosenPort, containerPort); err != nil {
				return err
			}

			// For theme images, copy the theme-specific CLAUDE.md file
			if isTheme {
				tmpfile, err := os.CreateTemp("", "claude-theme-*.md")
				if err != nil {
					return err
				}
				defer os.Remove(tmpfile.Name())

				if _, err := tmpfile.Write(assets.GetEmbeddedClaudeMdTheme()); err != nil {
					tmpfile.Close()
					return err
				}
				if err := tmpfile.Close(); err != nil {
					return err
				}

				if err := docker.CopyToContainer(name, tmpfile.Name(), "/var/www/CLAUDE.md"); err != nil {
					return fmt.Errorf("failed to copy theme documentation to container: %w", err)
				}
			}
			// give it a moment to boot services
			time.Sleep(500 * time.Millisecond)
		} else if !docker.Running(name) {
			fmt.Fprintf(cmd.OutOrStdout(), "Starting existing container '%s'...\n", name)
			if err := docker.Start(name); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Container '%s' is already running.\n", name)
		}

		// Remember container->image association
		if cfg.ContainerImages == nil {
			cfg.ContainerImages = map[string]string{}
		}
		if cfg.ContainerImages[name] != imgName {
			cfg.ContainerImages[name] = imgName
			_ = config.Save(configDir, cfg)
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Ready.")
		return nil
	},
}

func init() {
	startCmd.Flags().Bool("reset", false, "Stop and remove existing container before starting fresh")
	startCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
	startCmd.Flags().Int("host-starting-port", 0, "First host port to try for container port mapping")
	startCmd.Flags().Int("container-port", 0, "Container port to expose")
	startCmd.Flags().String("image", "", "Override image to start (defaults to selected image)")
}
