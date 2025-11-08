package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/assets"
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
			{label: "Github CLI", command: "npm install -g @github/copilot", runAsRoot: true},
			{label: "OpenCode AI", command: "npm install -g opencode-ai@latest", runAsRoot: true},
			{label: "Amp CLI", command: "npm install -g @sourcegraph/amp", runAsRoot: true},
			{label: "Claude CLI", command: "curl -fsSL https://claude.ai/install.sh | bash", useUserPaths: true},
			{label: "Aider", command: "curl -LsSf https://aider.chat/install.sh | sh", useUserPaths: true},
			{label: "Cursor Agent", command: "curl -fsS https://cursor.com/install | bash", useUserPaths: true},
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

var updateDiscourseCmd = &cobra.Command{
	Use:   "discourse",
	Short: "Update the Discourse image to latest using an embedded Dockerfile",
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

		// Determine which image to update (default: selected image)
		imageName, _ := cmd.Flags().GetString("image")
		if strings.TrimSpace(imageName) == "" {
			imageName = cfg.SelectedImage
		}
		imgCfg, ok := cfg.Images[imageName]
		if !ok {
			return fmt.Errorf("unknown image '%s'", imageName)
		}
		if imgCfg.Kind != "discourse" {
			return fmt.Errorf("'dv update discourse' only supports discourse-kind images; image '%s' is %q", imageName, imgCfg.Kind)
		}

		// Resolve the update Dockerfile to a local path
		dockerfilePath, contextDir, _, err := assets.ResolveDockerfileUpdateDiscourse(configDir)
		if err != nil {
			return err
		}

		// Build a temporary tag from the existing base image
		baseTag := imgCfg.Tag
		if strings.TrimSpace(baseTag) == "" {
			return fmt.Errorf("image '%s' has empty tag", imageName)
		}
		if !docker.ImageExists(baseTag) {
			return fmt.Errorf("base image tag '%s' does not exist; build it first with 'dv build'", baseTag)
		}

		// temp tag adds a suffix to avoid overwriting on failure
		tempTag := baseTag + ":updating"
		// If baseTag already contains a colon (repo:tag), preserve repo and use a separate temporary repo:tag if desired.
		// We will simply append -updated to the tag portion when colon is present.
		if strings.Contains(baseTag, ":") {
			// split on last colon
			idx := strings.LastIndex(baseTag, ":")
			repo := baseTag[:idx]
			tag := baseTag[idx+1:]
			tempTag = repo + ":" + tag + "-updating"
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Updating Discourse in image '%s' (tag %s) using %s...\n", imageName, baseTag, filepath.Base(dockerfilePath))

		// Build with BASE_IMAGE arg pointing at existing image tag
		buildArgs := []string{"--build-arg", "BASE_IMAGE=" + baseTag}
		cacheDir := ""
		if dir, err := defaultBuildCacheDir(baseTag); err == nil {
			cacheDir = dir
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Warning: unable to determine cache directory: %v\n", err)
		}
		opts := docker.BuildOptions{
			ExtraArgs: buildArgs,
			CacheDir:  cacheDir,
		}
		if err := docker.BuildFrom(tempTag, dockerfilePath, contextDir, opts); err != nil {
			return err
		}

		// Retag tempTag to baseTag (overwrite baseTag to point at updated image)
		fmt.Fprintf(cmd.OutOrStdout(), "Retagging %s -> %s...\n", tempTag, baseTag)
		if err := docker.TagImage(tempTag, baseTag); err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Discourse image updated to latest.")
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

	// dv update discourse
	updateCmd.AddCommand(updateDiscourseCmd)
	updateDiscourseCmd.Flags().String("image", "", "Image name to update (defaults to selected image)")
}
