package cli

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

type containerExecContext struct {
	name    string
	workdir string
	envs    []string
}

func prepareContainerExecContext(cmd *cobra.Command, overrideName ...string) (containerExecContext, bool, error) {
	configDir, err := xdg.ConfigDir()
	if err != nil {
		return containerExecContext{}, false, err
	}
	cfg, err := config.LoadOrCreate(configDir)
	if err != nil {
		return containerExecContext{}, false, err
	}

	name := ""
	if len(overrideName) > 0 && overrideName[0] != "" {
		name = overrideName[0]
	} else {
		name, _ = cmd.Flags().GetString("name")
		if name == "" {
			name = currentAgentName(cfg)
		}
	}
	if strings.TrimSpace(name) == "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "No container selected. Run 'dv start' first.")
		return containerExecContext{}, false, nil
	}

	if !docker.Exists(name) {
		fmt.Fprintf(cmd.OutOrStdout(), "Container '%s' does not exist. Run 'dv start' first.\n", name)
		return containerExecContext{}, false, nil
	}
	if !docker.Running(name) {
		fmt.Fprintf(cmd.OutOrStdout(), "Starting container '%s'...\n", name)
		if err := docker.Start(name); err != nil {
			return containerExecContext{}, false, err
		}
	}

	imgName := cfg.ContainerImages[name]
	var imgCfg config.ImageConfig
	if imgName != "" {
		imgCfg = cfg.Images[imgName]
	} else {
		_, imgCfg, err = resolveImage(cfg, "")
		if err != nil {
			return containerExecContext{}, false, err
		}
	}
	workdir := config.EffectiveWorkdir(cfg, imgCfg, name)

	copyConfiguredFiles(cmd, cfg, name, workdir, "")

	envs := collectEnvPassthrough(cfg)

	return containerExecContext{
		name:    name,
		workdir: workdir,
		envs:    envs,
	}, true, nil
}

func copyConfiguredFiles(cmd *cobra.Command, cfg config.Config, containerName, workdir, agent string) {
	agent = strings.ToLower(strings.TrimSpace(agent))
	for _, rule := range cfg.CopyRules {
		if !ruleMatchesAgent(rule, agent) {
			continue
		}
		hostPaths := expandHostSources(rule.Host)
		if len(hostPaths) == 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "Skipping copy (not found): %s -> %s\n", rule.Host, rule.Container)
			continue
		}
		for _, hostPath := range hostPaths {
			if hostPath == "" {
				continue
			}
			st, err := os.Stat(hostPath)
			if err != nil || !st.Mode().IsRegular() {
				fmt.Fprintf(cmd.ErrOrStderr(), "Skipping copy (not found): %s -> %s\n", hostPath, rule.Container)
				continue
			}

			target := containerPathFor(rule.Container, hostPath)
			dstDir := filepath.Dir(target)
			_, _ = docker.ExecOutput(containerName, workdir, []string{"bash", "-lc", "mkdir -p " + shellQuote(dstDir)})
			if err := docker.CopyToContainerWithOwnership(containerName, hostPath, target, false); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Failed to copy %s to %s: %v\n", hostPath, target, err)
				continue
			}
		}
	}
}

func collectEnvPassthrough(cfg config.Config) []string {
	envs := make([]string, 0, len(cfg.EnvPassthrough)+1)
	for _, key := range cfg.EnvPassthrough {
		if val, ok := os.LookupEnv(key); ok && val != "" {
			envs = append(envs, key)
		}
	}
	return envs
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

func expandHostSources(p string) []string {
	expanded := expandHostPath(p)
	if strings.ContainsAny(expanded, "*?[") {
		matches, err := filepath.Glob(expanded)
		if err != nil {
			return nil
		}
		return matches
	}
	return []string{expanded}
}

// shellQuote returns a single-quoted shell-safe string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func ruleMatchesAgent(rule config.CopyRule, agent string) bool {
	if len(rule.Agents) == 0 {
		return true
	}
	if agent == "" {
		return false
	}
	for _, a := range rule.Agents {
		if strings.EqualFold(strings.TrimSpace(a), agent) {
			return true
		}
	}
	return false
}

func containerPathFor(containerDst string, hostPath string) string {
	if strings.HasSuffix(containerDst, "/") {
		return path.Join(containerDst, filepath.Base(hostPath))
	}
	return containerDst
}
