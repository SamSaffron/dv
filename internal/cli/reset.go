package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

// resetCmd implements: dv reset [--name NAME]
// - Resets databases only (no code changes)
// - Stops discourse services, resets DB, migrates, seeds, and restarts services
var resetCmd = &cobra.Command{
	Use:   "reset [--name NAME]",
	Short: "Reset databases only (no code changes)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load config and container details
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

		if !docker.Exists(name) {
			fmt.Fprintf(cmd.OutOrStdout(), "Container '%s' does not exist. Run 'dv start' first.\n", name)
			return nil
		}
		if !docker.Running(name) {
			fmt.Fprintf(cmd.OutOrStdout(), "Starting container '%s'...\n", name)
			if err := docker.Start(name); err != nil {
				return err
			}
		}

		// Determine workdir from associated image
		imgName := cfg.ContainerImages[name]
		var imgCfg config.ImageConfig
		if imgName != "" {
			imgCfg = cfg.Images[imgName]
		} else {
			_, imgCfg, err = resolveImage(cfg, "")
			if err != nil {
				return err
			}
		}
		workdir := imgCfg.Workdir
		if strings.TrimSpace(workdir) == "" {
			workdir = "/var/www/discourse"
		}
		if imgCfg.Kind != "discourse" {
			return fmt.Errorf("'dv reset' is only supported for discourse image kind; current: %q", imgCfg.Kind)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Resetting databases in container '%s'...\n", name)

		// Build shell script for database reset only
		script := buildDiscourseDatabaseResetScript()

		// Run interactively to stream output to the user
		argv := []string{"bash", "-lc", script}
		if err := docker.ExecInteractive(name, workdir, nil, argv); err != nil {
			return fmt.Errorf("container: failed to reset databases: %w", err)
		}
		return nil
	},
}

func init() {
	resetCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
	rootCmd.AddCommand(resetCmd)
}
