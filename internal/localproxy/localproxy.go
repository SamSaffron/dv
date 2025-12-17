package localproxy

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dv/internal/config"
	"dv/internal/docker"
)

const (
	LabelEnabled    = "com.dv.local-proxy"
	LabelHost       = "com.dv.local-proxy.host"
	LabelTargetPort = "com.dv.local-proxy.target-port"
	LabelHTTPPort   = "com.dv.local-proxy.http-port"
	LabelHTTPSPort  = "com.dv.local-proxy.https-port"
)

var hostnameSanitizer = regexp.MustCompile(`[^a-z0-9-]`)

func HostnameForContainer(name string) string {
	base := strings.ToLower(strings.TrimSpace(name))
	base = strings.ReplaceAll(base, "_", "-")
	base = hostnameSanitizer.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-.")
	if base == "" {
		base = "dv"
	}
	return base + ".dv.localhost"
}

func Enabled(cfg config.Config) bool {
	return cfg.LocalProxy.Enabled
}

func Running(cfg config.LocalProxyConfig) bool {
	name := strings.TrimSpace(cfg.ContainerName)
	if name == "" {
		return false
	}
	return docker.Running(name)
}

func Healthy(cfg config.LocalProxyConfig, timeout time.Duration) error {
	client := newClient(cfg)
	done := time.After(timeout)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := client.Health(); err == nil {
			return nil
		}
		select {
		case <-done:
			return fmt.Errorf("local proxy API not responding on port %d", cfg.APIPort)
		case <-tick.C:
		}
	}
}

func RegisterRoute(cfg config.LocalProxyConfig, host string, target string) error {
	client := newClient(cfg)
	return client.Register(host, target)
}

func RemoveRoute(cfg config.LocalProxyConfig, host string) error {
	client := newClient(cfg)
	return client.Remove(host)
}

func RouteFromLabels(labels map[string]string) (host string, port int, httpPort int, ok bool) {
	host = strings.TrimSpace(labels[LabelHost])
	portStr := strings.TrimSpace(labels[LabelTargetPort])
	httpPortStr := strings.TrimSpace(labels[LabelHTTPPort])
	if host == "" || portStr == "" {
		return "", 0, 0, false
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p <= 0 {
		return "", 0, 0, false
	}
	httpPort, _ = strconv.Atoi(httpPortStr)
	return host, p, httpPort, true
}

func PortOccupied(port int) bool {
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
