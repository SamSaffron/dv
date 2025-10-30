package cli

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"dv/internal/assets"
	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var startCmd = &cobra.Command{
	Use:   "start [name] [--reset] [--image NAME] [--host-starting-port N] [--container-port N]",
	Short: "Create or start a container for the selected image",
	Args:  cobra.MaximumNArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Complete container name for the first positional argument
		if len(args) == 0 {
			return completeAgentNames(cmd, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
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
		// Priority: positional arg > --name flag > config
		name, _ := cmd.Flags().GetString("name")
		if len(args) > 0 {
			name = args[0]
		}
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
			labels := map[string]string{
				"com.dv.owner":      "dv",
				"com.dv.image-name": imgName,
				"com.dv.image-tag":  imageTag,
			}
			envs := map[string]string{
				"DISCOURSE_PORT": strconv.Itoa(chosenPort),
			}
			if err := docker.RunDetached(name, workdir, imageTag, chosenPort, containerPort, labels, envs); err != nil {
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
