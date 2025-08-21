package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var enterCmd = &cobra.Command{
	Use:   "enter [--name NAME] [-- cmd ...]",
	Short: "Enter the running container as user 'discourse' (or run a command)",
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

		// Determine workdir from the associated image if known; fall back to selected image
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

		return docker.ExecInteractive(name, workdir, envs, execArgs)
	},
}

func init() {
	enterCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
}
