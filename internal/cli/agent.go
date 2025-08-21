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

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage multiple agent containers",
}

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
	agentCmd.AddCommand(agentListCmd)
	agentCmd.AddCommand(agentNewCmd)
	agentNewCmd.Flags().String("image", "", "Image to use (defaults to selected image)")
	agentCmd.AddCommand(agentSelectCmd)
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
