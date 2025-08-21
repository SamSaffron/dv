package cli

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

// Legacy 'dv agent' command removed in favor of top-level commands.

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List containers created from the selected image",
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		imgName, imgCfg, err := resolveImage(cfg, "")
		if err != nil {
			return err
		}

		out, _ := runShell("docker ps -a --format '{{.Names}}\t{{.Image}}\t{{.Status}}'")
		selected := cfg.SelectedAgent
		printed := false
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) < 3 {
				continue
			}
			name, image, status := parts[0], parts[1], parts[2]
			if image != imgCfg.Tag {
				continue
			}
			mark := " "
			if selected != "" && name == selected {
				mark = "*"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\t%s\n", mark, name, status)
			printed = true
		}
		if !printed {
			fmt.Fprintf(cmd.OutOrStdout(), "(no agents found for image '%s')\n", imgCfg.Tag)
		}
		if selected != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Selected: %s\n", selected)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Selected: (none)")
		}
		_ = imgName // not printed but kept for clarity
		return nil
	},
}

var agentNewCmd = &cobra.Command{
	Use:   "new [NAME]",
	Short: "Create a new agent for the selected image and select it",
	Args:  cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		imageOverride, _ := cmd.Flags().GetString("image")
		name := ""
		if len(args) == 1 {
			name = args[0]
		} else {
			name = autogenName()
		}
		if docker.Exists(name) {
			return fmt.Errorf("an agent named '%s' already exists", name)
		}
		cfg.SelectedAgent = name

		// Determine which image to use
		imgName, imgCfg, err := resolveImage(cfg, imageOverride)
		if err != nil {
			return err
		}
		imageTag := imgCfg.Tag
		workdir := imgCfg.Workdir

		if err := config.Save(configDir, cfg); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Creating agent '%s' from image '%s'...\n", name, imageTag)
		// initialize container by running a no-op command
		if err := ensureContainerRunningWithWorkdir(cmd, cfg, name, workdir, imageTag, false); err != nil {
			return err
		}
		if cfg.ContainerImages == nil {
			cfg.ContainerImages = map[string]string{}
		}
		cfg.ContainerImages[name] = imgName
		_ = config.Save(configDir, cfg)
		fmt.Fprintf(cmd.OutOrStdout(), "Agent '%s' is ready and selected.\n", name)
		return nil
	},
}

var agentSelectCmd = &cobra.Command{
	Use:   "select NAME",
	Short: "Select an existing (or future) agent by name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		name := args[0]
		cfg.SelectedAgent = name
		if err := config.Save(configDir, cfg); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Selected agent: %s\n", name)
		return nil
	},
}

func init() {
	// Bind flags for top-level 'new'
	agentNewTopCmd.Flags().String("image", "", "Image to use (defaults to selected image)")
}

// Top-level convenience commands: `dv list`, `dv new`, `dv select`, `dv rename`
var agentListTopCmd = &cobra.Command{
	Use:   "list",
	Short: "List containers for the selected image (alias for 'agent list')",
	RunE:  agentListCmd.RunE,
}

var agentNewTopCmd = &cobra.Command{
	Use:   "new [NAME]",
	Short: "Create and select a new agent (alias for 'agent new')",
	Args:  agentNewCmd.Args,
	RunE:  agentNewCmd.RunE,
}

var agentSelectTopCmd = &cobra.Command{
	Use:   "select NAME",
	Short: "Select an agent (alias for 'agent select')",
	Args:  agentSelectCmd.Args,
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Complete NAME
		if len(args) == 0 {
			return completeAgentNames(cmd, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: agentSelectCmd.RunE,
}

var agentRenameTopCmd = &cobra.Command{
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

// completeAgentNames suggests existing container names for the selected image.
func completeAgentNames(cmd *cobra.Command, toComplete string) ([]string, cobra.ShellCompDirective) {
	configDir, err := xdg.ConfigDir()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := config.LoadOrCreate(configDir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	_, imgCfg, err := resolveImage(cfg, "")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out, _ := runShell("docker ps -a --format '{{.Names}}\t{{.Image}}'")
	var suggestions []string
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 2 {
			continue
		}
		name, image := parts[0], parts[1]
		if image != imgCfg.Tag {
			continue
		}
		if prefix == "" || strings.HasPrefix(strings.ToLower(name), prefix) {
			suggestions = append(suggestions, name)
		}
	}
	return suggestions, cobra.ShellCompDirectiveNoFileComp
}

func autogenName() string {
	return fmt.Sprintf("ai_agent_%s", time.Now().Format("20060102-150405"))
}

func runShell(script string) (string, error) {
	return execCombined("bash", "-lc", script)
}

func execCombined(name string, arg ...string) (string, error) {
	cmd := execCommand(name, arg...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

var execCommand = defaultExec

// indirection for testing
func defaultExec(name string, arg ...string) *exec.Cmd { return exec.Command(name, arg...) }

func containerImage(name string) (string, error) {
	out, err := runShell(fmt.Sprintf("docker inspect -f '{{.Config.Image}}' %s 2>/dev/null || true", name))
	return strings.TrimSpace(out), err
}

func ensureContainerRunning(cmd *cobra.Command, cfg config.Config, name string, reset bool) error {
	// Fallback: if container has a recorded image, use that; else use selected image
	imgName := cfg.ContainerImages[name]
	_, imgCfg, err := resolveImage(cfg, imgName)
	if err != nil {
		return err
	}
	workdir := imgCfg.Workdir
	imageTag := imgCfg.Tag
	return ensureContainerRunningWithWorkdir(cmd, cfg, name, workdir, imageTag, reset)
}

func ensureContainerRunningWithWorkdir(cmd *cobra.Command, cfg config.Config, name string, workdir string, imageTag string, reset bool) error {
	if reset && docker.Exists(name) {
		_ = docker.Stop(name)
		_ = docker.Remove(name)
	}
	if !docker.Exists(name) {
		// Choose the first available port starting from configured starting port
		chosenPort := cfg.HostStartingPort
		for isPortInUse(chosenPort) {
			chosenPort++
		}
		if err := docker.RunDetached(name, workdir, imageTag, chosenPort, cfg.ContainerPort); err != nil {
			return err
		}
	} else if !docker.Running(name) {
		if err := docker.Start(name); err != nil {
			return err
		}
	}
	return nil
}
