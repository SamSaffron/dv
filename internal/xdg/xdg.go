package xdg

import (
	"fmt"
	"os"
	"path/filepath"
)

func ConfigDir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "dv"), nil
}

func DataDir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "dv"), nil
}

func CacheDir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "dv"), nil
}

// RuntimeDir returns XDG_RUNTIME_DIR/dv or falls back to /tmp/dv-$UID.
// This directory is for ephemeral session data that should not persist across reboots.
func RuntimeDir() (string, error) {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "dv"), nil
	}
	// Fallback for systems without XDG_RUNTIME_DIR
	return filepath.Join(os.TempDir(), fmt.Sprintf("dv-%d", os.Getuid())), nil
}
