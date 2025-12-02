package assets

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed localproxy/Dockerfile
var embeddedLocalProxyDockerfile []byte

//go:embed localproxy/main.go
var embeddedLocalProxyMain []byte

//go:embed localproxy/go.mod.proxy
var embeddedLocalProxyGoMod []byte

// MaterializeLocalProxyContext writes the local proxy build context into
// <configDir>/local-proxy and returns the dockerfile path and context dir.
func MaterializeLocalProxyContext(configDir string) (dockerfilePath string, contextDir string, err error) {
	targetDir := filepath.Join(configDir, "local-proxy")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(targetDir, "Dockerfile"), embeddedLocalProxyDockerfile, 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(targetDir, "main.go"), embeddedLocalProxyMain, 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(targetDir, "go.mod"), embeddedLocalProxyGoMod, 0o644); err != nil {
		return "", "", err
	}
	return filepath.Join(targetDir, "Dockerfile"), targetDir, nil
}
