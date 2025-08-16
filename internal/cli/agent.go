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
	Short: "List containers created from the current image",
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil { return err }
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil { return err }

		// Use docker ps --filter ancestor to list; for simplicity, list all containers and filter names
		out, _ := runShell("docker ps -a --format '{{.Names}}\t{{.Image}}\t{{.Status}}'")
		selected := cfg.SelectedAgent
		printed := false
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if strings.TrimSpace(line) == "" { continue }
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) < 3 { continue }
			name, image, status := parts[0], parts[1], parts[2]
			if image != cfg.ImageTag { continue }
			mark := " "
			if selected != "" && name == selected { mark = "*" }
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\t%s\n", mark, name, status)
			printed = true
		}
		if !printed {
			fmt.Fprintf(cmd.OutOrStdout(), "(no agents found for image '%s')\n", cfg.ImageTag)
		}
		if selected != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Selected: %s\n", selected)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Selected: (none)")
		}
		return nil
	},
}

var agentNewCmd = &cobra.Command{
	Use:   "new [NAME]",
	Short: "Create a new agent and select it",
	Args:  cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil { return err }
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil { return err }

		name := ""
		if len(args) == 1 { name = args[0] } else { name = autogenName() }
		if docker.Exists(name) {
			return fmt.Errorf("an agent named '%s' already exists", name)
		}
		cfg.SelectedAgent = name
		if err := config.Save(configDir, cfg); err != nil { return err }
		fmt.Fprintf(cmd.OutOrStdout(), "Creating agent '%s' from image '%s'...\n", name, cfg.ImageTag)
		// initialize container by running a no-op command
		if err := ensureContainerRunning(cmd, cfg, name, false); err != nil { return err }
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
		if err != nil { return err }
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil { return err }

		name := args[0]
		// If container exists but uses different image, warn
		img, _ := containerImage(name)
		if img != "" && img != cfg.ImageTag {
			return fmt.Errorf("container '%s' exists but does not use image '%s'", name, cfg.ImageTag)
		}
		cfg.SelectedAgent = name
		if err := config.Save(configDir, cfg); err != nil { return err }
		fmt.Fprintf(cmd.OutOrStdout(), "Selected agent: %s\n", name)
		return nil
	},
}

func init() {
	agentCmd.AddCommand(agentListCmd)
	agentCmd.AddCommand(agentNewCmd)
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
        if err := docker.RunDetached(name, cfg.Workdir, cfg.ImageTag, chosenPort, cfg.ContainerPort); err != nil { return err }
	} else if !docker.Running(name) {
		if err := docker.Start(name); err != nil { return err }
	}
	return nil
}
