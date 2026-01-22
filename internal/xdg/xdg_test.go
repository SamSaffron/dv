package xdg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Note: These tests cannot use t.Parallel() because they use t.Setenv()

func TestConfigDir_UsesXDGConfigHome(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	dir, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	want := filepath.Join(tmpDir, "dv")
	if dir != want {
		t.Fatalf("expected %q, got %q", want, dir)
	}
}

func TestConfigDir_FallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")

	dir, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "dv")
	if dir != want {
		t.Fatalf("expected %q, got %q", want, dir)
	}
}

func TestDataDir_UsesXDGDataHome(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	want := filepath.Join(tmpDir, "dv")
	if dir != want {
		t.Fatalf("expected %q, got %q", want, dir)
	}
}

func TestDataDir_FallsBackToHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")

	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".local", "share", "dv")
	if dir != want {
		t.Fatalf("expected %q, got %q", want, dir)
	}
}

func TestCacheDir_UsesXDGCacheHome(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	dir, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	want := filepath.Join(tmpDir, "dv")
	if dir != want {
		t.Fatalf("expected %q, got %q", want, dir)
	}
}

func TestCacheDir_FallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")

	dir, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".cache", "dv")
	if dir != want {
		t.Fatalf("expected %q, got %q", want, dir)
	}
}

func TestRuntimeDir_UsesXDGRuntimeDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", tmpDir)

	dir, err := RuntimeDir()
	if err != nil {
		t.Fatalf("RuntimeDir: %v", err)
	}
	want := filepath.Join(tmpDir, "dv")
	if dir != want {
		t.Fatalf("expected %q, got %q", want, dir)
	}
}

func TestRuntimeDir_FallsBackToTmp(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")

	dir, err := RuntimeDir()
	if err != nil {
		t.Fatalf("RuntimeDir: %v", err)
	}
	// Should be /tmp/dv-$UID (or os.TempDir()/dv-$UID)
	if !strings.HasPrefix(dir, os.TempDir()) {
		t.Fatalf("expected dir to start with temp dir, got %q", dir)
	}
	if !strings.Contains(dir, "dv-") {
		t.Fatalf("expected dir to contain 'dv-', got %q", dir)
	}
}
