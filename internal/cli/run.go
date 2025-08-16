package cli

import (
    "fmt"
    "net"
    "os"
    "time"

    "github.com/spf13/cobra"

    "dv/internal/config"
    "dv/internal/docker"
    "dv/internal/xdg"
)

var runCmd = &cobra.Command{
    Use:   "run [--reset] [--name NAME] [--host-starting-port N] [--container-port N] [-- cmd ...]",
	Short: "Run or attach to the container",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil { return err }
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil { return err }

		reset, _ := cmd.Flags().GetBool("reset")
		name, _ := cmd.Flags().GetString("name")
		if name == "" { name = currentAgentName(cfg) }

        hostPort, _ := cmd.Flags().GetInt("host-starting-port")
		containerPort, _ := cmd.Flags().GetInt("container-port")
        if hostPort == 0 { hostPort = cfg.HostStartingPort }
		if containerPort == 0 { containerPort = cfg.ContainerPort }

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
            fmt.Fprintf(cmd.OutOrStdout(), "Creating and starting container '%s'...\n", name)
            if err := docker.RunDetached(name, cfg.Workdir, cfg.ImageTag, chosenPort, containerPort); err != nil { return err }
			// give it a moment to boot services
			time.Sleep(500 * time.Millisecond)
		} else if !docker.Running(name) {
			fmt.Fprintf(cmd.OutOrStdout(), "Starting existing container '%s'...\n", name)
			if err := docker.Start(name); err != nil { return err }
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
		if len(args) > 0 { execArgs = args }

		return docker.ExecInteractive(name, cfg.Workdir, envs, execArgs)
	},
}

func init() {
	runCmd.Flags().Bool("reset", false, "Stop and remove existing container before starting fresh")
	runCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
    runCmd.Flags().Int("host-starting-port", 0, "First host port to try for container port mapping")
	runCmd.Flags().Int("container-port", 0, "Container port to expose")
}

func isPortInUse(port int) bool {
    l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
    if err != nil {
        return true
    }
    _ = l.Close()
    return false
}
