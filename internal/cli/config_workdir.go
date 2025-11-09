package cli

import (
	"fmt"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/xdg"
)

var configWorkdirCmd = &cobra.Command{
	Use:   "workdir [PATH]",
	Short: "Show or override the per-container workdir used by dv run/enter/extract",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reset, _ := cmd.Flags().GetBool("reset")
		if reset && len(args) > 0 {
			return fmt.Errorf("cannot supply PATH while using --reset")
		}

		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		containerOverride, _ := cmd.Flags().GetString("container")
		containerName := strings.TrimSpace(containerOverride)
		if containerName == "" {
			containerName = currentAgentName(cfg)
		}
		if strings.TrimSpace(containerName) == "" {
			return fmt.Errorf("no container selected; use --container or run 'dv start'")
		}

		imgName := cfg.ContainerImages[containerName]
		displayImage := imgName
		if strings.TrimSpace(displayImage) == "" {
			displayImage = cfg.SelectedImage
		}
		_, imgCfg, err := resolveImage(cfg, imgName)
		if err != nil {
			return err
		}
		imageWorkdir := strings.TrimSpace(imgCfg.Workdir)
		if imageWorkdir == "" {
			imageWorkdir = "/var/www/discourse"
		}

		if reset {
			removed := false
			if cfg.CustomWorkdirs != nil {
				if _, ok := cfg.CustomWorkdirs[containerName]; ok {
					delete(cfg.CustomWorkdirs, containerName)
					removed = true
				}
			}
			if !removed {
				fmt.Fprintf(cmd.OutOrStdout(), "No override set for container %s.\n", containerName)
				return nil
			}
			if err := config.Save(configDir, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Cleared workdir override for container %s; image defaults restored.\n", containerName)
			return nil
		}

		if len(args) == 0 {
			override := ""
			if cfg.CustomWorkdirs != nil {
				override = strings.TrimSpace(cfg.CustomWorkdirs[containerName])
			}
			effective := config.EffectiveWorkdir(cfg, imgCfg, containerName)

			fmt.Fprintf(cmd.OutOrStdout(), "Container: %s\n", containerName)
			fmt.Fprintf(cmd.OutOrStdout(), "Image: %s (workdir %s)\n", displayImage, imageWorkdir)
			if override == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "Override: (not set)")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Override: %s\n", override)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Effective workdir: %s\n", effective)
			return nil
		}

		newWorkdir := strings.TrimSpace(args[0])
		if !strings.HasPrefix(newWorkdir, "/") {
			return fmt.Errorf("workdir must be an absolute path inside the container")
		}
		cleaned := path.Clean(newWorkdir)

		if cfg.CustomWorkdirs == nil {
			cfg.CustomWorkdirs = map[string]string{}
		}
		cfg.CustomWorkdirs[containerName] = cleaned
		if err := config.Save(configDir, cfg); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Workdir override for %s set to %s\n", containerName, cleaned)
		fmt.Fprintf(cmd.OutOrStdout(), "Future 'dv run', 'dv enter', 'dv run-agent', and 'dv extract' commands targeting %s will use this path.\n", containerName)
		return nil
	},
}

func init() {
	configWorkdirCmd.Flags().Bool("reset", false, "Remove the override and fall back to the image workdir")
	configWorkdirCmd.Flags().String("container", "", "Container to inspect or modify (defaults to the selected agent)")
	configCmd.AddCommand(configWorkdirCmd)
}
