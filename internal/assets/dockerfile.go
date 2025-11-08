package assets

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// We embed a copy of the repository's Dockerfile (kept in this package
// directory) so the binary can materialize it into the XDG config directory
// if needed.
//
//go:embed Dockerfile
var embeddedDockerfile []byte

//go:embed Dockerfile.update.discourse
var embeddedDockerfileUpdateDiscourse []byte

// EmbeddedDockerfileSHA256 returns the hex-encoded SHA-256 of the embedded Dockerfile.
func EmbeddedDockerfileSHA256() string {
	sum := sha256.Sum256(embeddedDockerfile)
	return hex.EncodeToString(sum[:])
}

// ResolveDockerfile determines which Dockerfile to use and ensures it exists.
// Priority:
// 1) Environment variable DV_DOCKERFILE points to an existing file
// 2) A user-provided override at <configDir>/Dockerfile.local
// 3) The embedded Dockerfile, extracted to <configDir>/Dockerfile if missing or outdated
// It returns (dockerfilePath, contextDir, usedOverride, error)
func ResolveDockerfile(configDir string) (string, string, bool, error) {
	return resolveDockerfileInternal(configDir, "Dockerfile", embeddedDockerfile, EmbeddedDockerfileSHA256())
}

// EmbeddedDockerfileUpdateDiscourseSHA256 returns the hex-encoded SHA-256 of the embedded update Dockerfile.
func EmbeddedDockerfileUpdateDiscourseSHA256() string {
	sum := sha256.Sum256(embeddedDockerfileUpdateDiscourse)
	return hex.EncodeToString(sum[:])
}

// ResolveDockerfileUpdateDiscourse resolves the update Dockerfile used by `dv update discourse`.
// Priority:
// 1) DV_DOCKERFILE_UPDATE_DISCOURSE env var
// 2) <configDir>/Dockerfile.update.discourse.local
// 3) embedded Dockerfile.update.discourse at <configDir>/Dockerfile.update.discourse
func ResolveDockerfileUpdateDiscourse(configDir string) (string, string, bool, error) {
	return resolveDockerfileInternal(configDir, "Dockerfile.update.discourse", embeddedDockerfileUpdateDiscourse, EmbeddedDockerfileUpdateDiscourseSHA256())
}

// resolveDockerfileInternal is a helper function to resolve Dockerfiles with different names
func resolveDockerfileInternal(configDir string, fileName string, embeddedContent []byte, embeddedSHA string) (string, string, bool, error) {
	// Env override takes precedence
	envVar := "DV_DOCKERFILE"
	if fileName == "Dockerfile.update.discourse" {
		envVar = "DV_DOCKERFILE_UPDATE_DISCOURSE"
	}
	if envPath, ok := os.LookupEnv(envVar); ok && envPath != "" {
		if info, err := os.Stat(envPath); err == nil && !info.IsDir() {
			return envPath, filepath.Dir(envPath), true, nil
		}
		return "", "", false, fmt.Errorf("%s path does not exist: %s", envVar, envPath)
	}

	// Local override in config directory
	localSuffix := ".local"
	localOverride := filepath.Join(configDir, fileName+localSuffix)
	if info, err := os.Stat(localOverride); err == nil && !info.IsDir() {
		return localOverride, configDir, true, nil
	}

	// Fallback to embedded Dockerfile with SHA-based update
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return "", "", false, err
	}
	targetPath := filepath.Join(configDir, fileName)
	shaPath := filepath.Join(configDir, fileName+".sha256")

	// Use the provided SHA from the calling function
	needWrite := false

	// Decide whether to write/update the Dockerfile
	if b, err := os.ReadFile(shaPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			needWrite = true
		} else {
			return "", "", false, err
		}
	} else if string(trimSpaceBytes(b)) != embeddedSHA {
		needWrite = true
	}

	if needWrite {
		if err := os.WriteFile(targetPath, embeddedContent, 0o644); err != nil {
			return "", "", false, err
		}
		if err := os.WriteFile(shaPath, []byte(embeddedSHA+"\n"), 0o644); err != nil {
			return "", "", false, err
		}
	}

	return targetPath, configDir, false, nil
}

func trimSpaceBytes(b []byte) []byte {
	// minimal trim to avoid allocating strings while comparing
	start, end := 0, len(b)
	for start < end {
		c := b[start]
		if c == ' ' || c == '\n' || c == '\r' || c == '\t' {
			start++
			continue
		}
		break
	}
	for end > start {
		c := b[end-1]
		if c == ' ' || c == '\n' || c == '\r' || c == '\t' {
			end--
			continue
		}
		break
	}
	return b[start:end]
}
