package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update tooling inside the container",
}

var updateAgentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "Update all AI agents inside the container",
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

		name, _ := cmd.Flags().GetString("name")
		if name == "" {
			name = currentAgentName(cfg)
		}
		if name == "" {
			return fmt.Errorf("no agent selected; run 'dv start' to create one")
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

		imgCfg, err := resolveImageConfig(cfg, name)
		if err != nil {
			return err
		}
		workdir := imgCfg.Workdir
		if workdir == "" {
			workdir = "/var/www/discourse"
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Updating AI agents in container '%s'...\n", name)

		steps := []agentUpdateStep{
			{label: "OpenAI Codex CLI", command: "npm install -g @openai/codex", runAsRoot: true},
			{label: "Google Gemini CLI", command: "npm install -g @google/gemini-cli", runAsRoot: true},
			{label: "Crush CLI", command: "npm install -g @charmland/crush", runAsRoot: true},
			{label: "Claude CLI", command: "curl -fsSL https://claude.ai/install.sh | bash", useUserPaths: true},
			{label: "Aider", command: "curl -LsSf https://aider.chat/install.sh | sh", useUserPaths: true},
			{label: "Cursor Agent", command: "curl -fsS https://cursor.com/install | bash", useUserPaths: true},
			{label: "OpenCode Agent", command: "curl -fsS https://opencode.ai/install | bash", useUserPaths: true},
		}

		for _, step := range steps {
			if err := runAgentUpdateStep(cmd, name, workdir, step); err != nil {
				return err
			}
		}

		fmt.Fprintln(cmd.OutOrStdout(), "All agents updated.")
		return nil
	},
}

type agentUpdateStep struct {
	label        string
	command      string
	runAsRoot    bool
	useUserPaths bool
}

func runAgentUpdateStep(cmd *cobra.Command, containerName, workdir string, step agentUpdateStep) error {
	fmt.Fprintf(cmd.OutOrStdout(), "• %s...\n", step.label)

	shellCmd := "set -euo pipefail; "
	if step.useUserPaths {
		shellCmd += withUserPaths(step.command)
	} else {
		shellCmd += step.command
	}

	argv := []string{"bash", "-lc", shellCmd}
	var err error
	if step.runAsRoot {
		err = docker.ExecInteractiveAsRoot(containerName, workdir, nil, argv)
	} else {
		err = docker.ExecInteractive(containerName, workdir, nil, argv)
	}
	if err != nil {
		return fmt.Errorf("failed to update %s: %w", step.label, err)
	}
	return nil
}

func resolveImageConfig(cfg config.Config, containerName string) (config.ImageConfig, error) {
	if imgName, ok := cfg.ContainerImages[containerName]; ok {
		if imgCfg, found := cfg.Images[imgName]; found {
			return imgCfg, nil
		}
	}
	_, imgCfg, err := resolveImage(cfg, "")
	if err != nil {
		return config.ImageConfig{}, err
	}
	return imgCfg, nil
}

func init() {
	updateCmd.AddCommand(updateAgentsCmd)
	updateAgentsCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
}
