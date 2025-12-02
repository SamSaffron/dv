package localproxy

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"dv/internal/assets"
	"dv/internal/config"
	"dv/internal/docker"
)

func BuildImage(configDir string, cfg config.LocalProxyConfig) error {
	dockerfile, contextDir, err := assets.MaterializeLocalProxyContext(configDir)
	if err != nil {
		return err
	}
	return docker.BuildFrom(cfg.ImageTag, dockerfile, contextDir, docker.BuildOptions{})
}

func EnsureContainer(cfg config.LocalProxyConfig, recreate bool) error {
	name := strings.TrimSpace(cfg.ContainerName)
	if name == "" {
		return fmt.Errorf("local proxy container name is empty")
	}

	if cfg.HTTPPort == cfg.APIPort {
		return fmt.Errorf("http and api ports must differ")
	}

	if recreate && docker.Exists(name) {
		_ = docker.Stop(name)
		_ = docker.Remove(name)
	}

	if docker.Exists(name) {
		if docker.Running(name) {
			return nil
		}
		return docker.Start(name)
	}

	if PortOccupied(cfg.HTTPPort) {
		return fmt.Errorf("host port %d is already in use", cfg.HTTPPort)
	}
	if PortOccupied(cfg.APIPort) {
		return fmt.Errorf("host port %d is already in use", cfg.APIPort)
	}

	args := []string{
		"run", "-d",
		"--name", name,
		"-p", fmt.Sprintf("%d:%d", cfg.HTTPPort, 80),
		"-p", fmt.Sprintf("%d:%d", cfg.APIPort, 2080),
		"--add-host", "host.docker.internal:host-gateway",
		"--label", "com.dv.owner=dv",
		"--label", LabelEnabled + "=true",
		"--label", LabelHTTPPort + "=" + strconv.Itoa(cfg.HTTPPort),
	}

	args = append(args, "-e", "PROXY_HTTP_ADDR=:80")
	args = append(args, "-e", "PROXY_API_ADDR=:2080")

	args = append(args, cfg.ImageTag)

	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
