package cli

import (
	"fmt"
	"path"
	"strings"

	"dv/internal/config"
)

// setContainerWorkdir stores a custom workdir override for a container and
// persists the updated config to disk.
func setContainerWorkdir(cfg *config.Config, configDir, containerName, workdir string) error {
	cleaned := path.Clean(workdir)
	if !strings.HasPrefix(cleaned, "/") {
		return fmt.Errorf("expected absolute path, got %s", cleaned)
	}
	if cfg.CustomWorkdirs == nil {
		cfg.CustomWorkdirs = map[string]string{}
	}
	cfg.CustomWorkdirs[containerName] = cleaned
	return config.Save(configDir, *cfg)
}
