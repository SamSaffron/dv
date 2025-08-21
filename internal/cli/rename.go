package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var renameCmd = &cobra.Command{
	Use:   "rename OLD NEW",
	Short: "Rename an existing agent container",
	Args:  cobra.ExactArgs(2),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Complete OLD when providing the first arg, NEW is free text
		if len(args) == 0 {
			return completeAgentNames(cmd, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		oldName := strings.TrimSpace(args[0])
		newName := strings.TrimSpace(args[1])
		if oldName == "" || newName == "" {
			return fmt.Errorf("invalid names")
		}
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}
		if !docker.Exists(oldName) {
			return fmt.Errorf("agent '%s' does not exist", oldName)
		}
		if docker.Exists(newName) {
			return fmt.Errorf("an agent named '%s' already exists", newName)
		}
		if err := docker.Rename(oldName, newName); err != nil {
			return err
		}
		// Update selection and mappings
		if cfg.SelectedAgent == oldName {
			cfg.SelectedAgent = newName
		}
		if cfg.ContainerImages != nil {
			if img, ok := cfg.ContainerImages[oldName]; ok {
				delete(cfg.ContainerImages, oldName)
				cfg.ContainerImages[newName] = img
			}
		}
		if err := config.Save(configDir, cfg); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Renamed agent '%s' -> '%s'\n", oldName, newName)
		return nil
	},
}
