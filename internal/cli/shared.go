package cli

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

// isTruthyEnv returns true for truthy environment variable values.
func isTruthyEnv(key string) bool {
	val := strings.TrimSpace(os.Getenv(key))
	switch strings.ToLower(val) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func currentAgentName(cfg config.Config) string {
	name := cfg.SelectedAgent
	if name == "" {
		name = cfg.DefaultContainer
	}
	return name
}

func getenv(keys ...string) []string {
	var out []string
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			out = append(out, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return out
}

// resolveImage returns the image name and config, given an optional override name.
// If override is empty, the currently selected image is used.
func resolveImage(cfg config.Config, override string) (string, config.ImageConfig, error) {
	name := override
	if name == "" {
		name = cfg.SelectedImage
	}
	img, ok := cfg.Images[name]
	if !ok {
		return "", config.ImageConfig{}, fmt.Errorf("unknown image '%s'", name)
	}
	return name, img, nil
}

// isPortInUse returns true when the given TCP port cannot be bound on localhost
// or is already allocated to a Docker container.
func isPortInUse(port int, dockerAllocated map[int]bool) bool {
	if dockerAllocated != nil && dockerAllocated[port] {
		if isTruthyEnv("DV_VERBOSE") {
			fmt.Fprintf(os.Stderr, "Port %d is already allocated by a Docker container\n", port)
		}
		return true
	}
	// Try to listen on all interfaces. This is the most conservative check.
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		if isTruthyEnv("DV_VERBOSE") {
			fmt.Fprintf(os.Stderr, "Port %d is in use (Listen :%d failed: %v)\n", port, port, err)
		}
		return true
	}
	_ = l.Close()

	// Also specifically check 127.0.0.1 and [::1] because sometimes ':' only
	// binds to one of them depending on system configuration.
	for _, host := range []string{"127.0.0.1", "[::1]"} {
		l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
		if err != nil {
			if isTruthyEnv("DV_VERBOSE") {
				fmt.Fprintf(os.Stderr, "Port %d is in use (Listen %s:%d failed: %v)\n", port, host, port, err)
			}
			return true
		}
		_ = l.Close()
	}

	return false
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
	out, _ := runShell("docker ps -a --format '{{.Names}}\t{{.Image}}\t{{.Labels}}'")
	var suggestions []string
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		name, image := parts[0], parts[1]
		labelsField := ""
		if len(parts) >= 3 {
			labelsField = parts[2]
		}
		belongs := false
		if imgNameFromCfg, ok := cfg.ContainerImages[name]; ok && imgNameFromCfg == cfg.SelectedImage {
			belongs = true
		}
		if !belongs {
			if labelMap := parseLabels(labelsField); labelMap["com.dv.owner"] == "dv" && labelMap["com.dv.image-name"] == cfg.SelectedImage {
				belongs = true
			}
		}
		if !belongs {
			if image == imgCfg.Tag {
				belongs = true
			}
		}
		if !belongs {
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

// shellQuote returns a single-quoted shell-safe string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// shellJoin quotes argv for safe execution in a single shell command.
func shellJoin(argv []string) string {
	quoted := make([]string, 0, len(argv))
	for _, a := range argv {
		quoted = append(quoted, shellQuote(a))
	}
	return strings.Join(quoted, " ")
}

func ensureContainerRunning(cmd *cobra.Command, cfg config.Config, name string, reset bool, sshAuthSock string) error {
	// Fallback: if container has a recorded image, use that; else use selected image
	imgName := cfg.ContainerImages[name]
	_, imgCfg, err := resolveImage(cfg, imgName)
	if err != nil {
		return err
	}
	workdir := imgCfg.Workdir
	imageTag := imgCfg.Tag
	return ensureContainerRunningWithWorkdir(cmd, cfg, name, workdir, imageTag, imgName, reset, sshAuthSock)
}

func ensureContainerRunningWithWorkdir(cmd *cobra.Command, cfg config.Config, name string, workdir string, imageTag string, imgName string, reset bool, sshAuthSock string) error {
	if reset && docker.Exists(name) {
		_ = docker.Stop(name)
		_ = docker.Remove(name)
	}
	if !docker.Exists(name) {
		// Choose the first available port starting from configured starting port
		allocated, err := docker.AllocatedPorts()
		if err != nil {
			if isTruthyEnv("DV_VERBOSE") {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to detect allocated Docker ports: %v\n", err)
			}
		}
		chosenPort := cfg.HostStartingPort
		if isTruthyEnv("DV_VERBOSE") {
			fmt.Fprintf(cmd.OutOrStdout(), "Searching for an available port starting from %d...\n", chosenPort)
		}
		for isPortInUse(chosenPort, allocated) {
			chosenPort++
		}
		if isTruthyEnv("DV_VERBOSE") {
			fmt.Fprintf(cmd.OutOrStdout(), "Selected port %d.\n", chosenPort)
		}
		labels := map[string]string{
			"com.dv.owner":      "dv",
			"com.dv.image-name": imgName,
			"com.dv.image-tag":  imageTag,
		}
		envs := map[string]string{
			"DISCOURSE_PORT": strconv.Itoa(chosenPort),
		}
		extraHosts := []string{}
		proxyHost := applyLocalProxyMetadata(cfg, name, chosenPort, cfg.ContainerPort, labels, envs)
		if proxyHost != "" {
			extraHosts = append(extraHosts, fmt.Sprintf("%s:127.0.0.1", proxyHost))
		}
		if err := docker.RunDetached(name, workdir, imageTag, chosenPort, cfg.ContainerPort, labels, envs, extraHosts, sshAuthSock); err != nil {
			return err
		}
		if proxyHost != "" {
			registerWithLocalProxy(cmd, cfg, name, proxyHost, cfg.ContainerPort)
		}
	} else if !docker.Running(name) {
		if err := docker.Start(name); err != nil {
			return err
		}
		registerContainerFromLabels(cmd, cfg, name)
	} else {
		registerContainerFromLabels(cmd, cfg, name)
	}
	return nil
}
