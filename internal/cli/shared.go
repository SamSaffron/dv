package cli

import (
	"fmt"
	"net"
	"os"

	"dv/internal/config"
)

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

// isPortInUse returns true when the given TCP port cannot be bound on localhost.
func isPortInUse(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	_ = l.Close()
	return false
}
