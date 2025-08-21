package cli

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/spf13/cobra"

	"dv/internal/assets"
	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var runCmd = &cobra.Command{
	Use:   "run [--reset] [--name NAME] [--image NAME] [--host-starting-port N] [--container-port N] [-- cmd ...]",
	Short: "Run or attach to a container for the selected image",
	Args:  cobra.ArbitraryArgs,
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
				// Create temp file with theme CLAUDE.md content
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

				// Copy the file into the container
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
		}

		// Prepare env pass-through
		envs := make([]string, 0, len(cfg.EnvPassthrough)+1)
		envs = append(envs, "CI=1")
		for _, key := range cfg.EnvPassthrough {
			if val, ok := os.LookupEnv(key); ok && val != "" {
				envs = append(envs, key)
			}
		}

		execArgs := []string{"bash", "-l"}
		for i, a := range args {
			if a == "--" {
				args = args[i+1:]
				break
			}
		}
		if len(args) > 0 {
			execArgs = args
		}

		// Remember container->image association
		if cfg.ContainerImages == nil {
			cfg.ContainerImages = map[string]string{}
		}
		if cfg.ContainerImages[name] != imgName {
			cfg.ContainerImages[name] = imgName
			_ = config.Save(configDir, cfg)
		}
		return docker.ExecInteractive(name, workdir, envs, execArgs)
	},
}

func init() {
	runCmd.Flags().Bool("reset", false, "Stop and remove existing container before starting fresh")
	runCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
	runCmd.Flags().Int("host-starting-port", 0, "First host port to try for container port mapping")
	runCmd.Flags().Int("container-port", 0, "Container port to expose")
	runCmd.Flags().String("image", "", "Override image to run (defaults to selected image)")
}

func isPortInUse(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	_ = l.Close()
	return false
}
