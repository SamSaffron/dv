package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

		// Before entering, copy any configured files into the container
		for hostSrc, containerDst := range cfg.CopyFiles {
			hostPath := expandHostPath(hostSrc)
			if hostPath == "" {
				continue
			}
			if st, err := os.Stat(hostPath); err != nil || !st.Mode().IsRegular() {
				// Skip missing or non-regular files
				fmt.Fprintf(cmd.ErrOrStderr(), "Skipping copy (not found): %s -> %s\n", hostPath, containerDst)
				continue
			}
			// Ensure destination directory exists inside container (as discourse user)
			dstDir := filepath.Dir(containerDst)
			_, _ = docker.ExecOutput(name, workdir, []string{"bash", "-lc", "mkdir -p " + shellQuote(dstDir)})
			// Copy file
			if err := docker.CopyToContainer(name, hostPath, containerDst); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Failed to copy %s to %s: %v\n", hostPath, containerDst, err)
				continue
			}
			// Ensure ownership so 'discourse' can read
			_, _ = docker.ExecAsRoot(name, workdir, []string{"chown", "discourse:discourse", containerDst})
		}

		// Prepare env pass-through
		envs := make([]string, 0, len(cfg.EnvPassthrough)+1)
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

// expandHostPath expands a host path allowing ~ and environment variables.
func expandHostPath(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				p = home
			} else if strings.HasPrefix(p, "~/") {
				p = filepath.Join(home, p[2:])
			}
		}
	}
	p = os.ExpandEnv(p)
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// shellQuote returns a single-quoted shell-safe string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func init() {
	enterCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
}
