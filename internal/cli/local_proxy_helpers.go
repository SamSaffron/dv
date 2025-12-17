package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/localproxy"
)

func applyLocalProxyMetadata(cfg config.Config, containerName string, hostPort int, labels map[string]string, envs map[string]string) string {
	if !cfg.LocalProxy.Enabled || hostPort <= 0 {
		return ""
	}
	lp := cfg.LocalProxy
	lp.ApplyDefaults()
	if !localproxy.Running(lp) {
		return ""
	}

	host := localproxy.HostnameForContainer(containerName)
	labels[localproxy.LabelEnabled] = "true"
	labels[localproxy.LabelHost] = host
	labels[localproxy.LabelTargetPort] = strconv.Itoa(hostPort)
	labels[localproxy.LabelHTTPPort] = strconv.Itoa(lp.HTTPPort)
	if lp.HTTPS {
		labels[localproxy.LabelHTTPSPort] = strconv.Itoa(lp.HTTPSPort)
	}

	envs["DISCOURSE_HOSTNAME"] = host
	envs["DV_LOCAL_PROXY_HOST"] = host
	envs["DV_LOCAL_PROXY_HTTP_PORT"] = strconv.Itoa(lp.HTTPPort)
	if lp.HTTPS {
		envs["DV_LOCAL_PROXY_SCHEME"] = "https"
		envs["DV_LOCAL_PROXY_PORT"] = strconv.Itoa(lp.HTTPSPort)
		envs["DV_LOCAL_PROXY_HTTPS_PORT"] = strconv.Itoa(lp.HTTPSPort)
		envs["DISCOURSE_FORCE_HTTPS"] = "true"
		// Override DISCOURSE_PORT so URLs use the external HTTPS port, not the internal one
		envs["DISCOURSE_PORT"] = strconv.Itoa(lp.HTTPSPort)
	} else {
		envs["DV_LOCAL_PROXY_SCHEME"] = "http"
		envs["DV_LOCAL_PROXY_PORT"] = strconv.Itoa(lp.HTTPPort)
		// Override DISCOURSE_PORT so URLs use the external HTTP port, not the internal one
		envs["DISCOURSE_PORT"] = strconv.Itoa(lp.HTTPPort)
	}

	return host
}

func registerWithLocalProxy(cmd *cobra.Command, cfg config.Config, host string, hostPort int) {
	if host == "" || hostPort <= 0 || !cfg.LocalProxy.Enabled {
		return
	}
	lp := cfg.LocalProxy
	lp.ApplyDefaults()
	if !localproxy.Running(lp) {
		return
	}
	target := fmt.Sprintf("http://host.docker.internal:%d", hostPort)
	if err := localproxy.RegisterRoute(lp, host, target); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Failed to register %s at %s: %v\n", host, target, err)
	}
}

func registerContainerFromLabels(cmd *cobra.Command, cfg config.Config, name string) {
	if !cfg.LocalProxy.Enabled {
		return
	}
	labels, err := docker.Labels(name)
	if err != nil {
		return
	}
	host, port, _, ok := localproxy.RouteFromLabels(labels)
	if !ok {
		return
	}
	registerWithLocalProxy(cmd, cfg, host, port)
}
