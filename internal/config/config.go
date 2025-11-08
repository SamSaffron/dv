package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	ImageTag         string `json:"imageTag"`
	DefaultContainer string `json:"defaultContainerName"`
	Workdir          string `json:"workdir"`
	// HostStartingPort is the first port to try on the host.
	HostStartingPort    int      `json:"hostStartingPort"`
	ContainerPort       int      `json:"containerPort"`
	SelectedAgent       string   `json:"selectedAgent"`
	EnvPassthrough      []string `json:"envPassthrough"`
	DiscourseRepo       string   `json:"discourseRepo"`
	ExtractBranchPrefix string   `json:"extractBranchPrefix"`

	// New image model (supersedes legacy fields above)
	// SelectedImage is the name of the currently selected image (must always be set)
	SelectedImage string `json:"selectedImage"`
	// Images is a registry of named images and their metadata
	Images map[string]ImageConfig `json:"images"`
	// ContainerImages maps container name -> image name for provenance
	ContainerImages map[string]string `json:"containerImages"`

	// CopyFiles maps host source paths to container destination paths that
	// should be copied into the container at `dv enter` time. Host paths may
	// include `~` for home and environment variables; they are expanded at
	// runtime. Keys are host paths, values are container paths.
	CopyFiles map[string]string `json:"copyFiles"`
}

// ImageSource describes how to obtain the Dockerfile for an image.
type ImageSource struct {
	// Source is one of: "stock" | "path"
	Source string `json:"source"`
	// StockName is valid when Source=="stock": "discourse"
	StockName string `json:"stockName,omitempty"`
	// Path is valid when Source=="path": absolute or relative path to Dockerfile
	Path string `json:"path,omitempty"`
}

// ImageConfig is the per-image configuration.
type ImageConfig struct {
	// Kind drives special behavior in the CLI: "discourse" | "custom"
	Kind          string      `json:"kind"`
	Tag           string      `json:"tag"`
	Workdir       string      `json:"workdir"`
	ContainerPort int         `json:"containerPort"`
	Dockerfile    ImageSource `json:"dockerfile"`
}

func Default() Config {
	return Config{
		ImageTag:         "ai_agent",
		DefaultContainer: "ai_agent",
		Workdir:          "/var/www/discourse",
		HostStartingPort: 4200,
		ContainerPort:    4200,
		EnvPassthrough: []string{
			"CURSOR_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
			"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
			"CLAUDE_CODE_USE_BEDROCK", "DEEPSEEK_API_KEY", "GEMINI_API_KEY",
			"AMP_API_KEY", "GH_TOKEN", "OPENROUTER_API_KEY",
		},
		DiscourseRepo:       "https://github.com/discourse/discourse.git",
		ExtractBranchPrefix: "agent-changes",
		// New image model defaults
		SelectedImage: "discourse",
		Images: map[string]ImageConfig{
			"discourse": {
				Kind:          "discourse",
				Tag:           "ai_agent",
				Workdir:       "/var/www/discourse",
				ContainerPort: 4200,
				Dockerfile:    ImageSource{Source: "stock", StockName: "discourse"},
			},
		},
		ContainerImages: map[string]string{},
		CopyFiles: map[string]string{
			"~/.codex/auth.json": "/home/discourse/.codex/auth.json",
		},
	}
}

func Path(dir string) string { return filepath.Join(dir, "config.json") }

func LoadOrCreate(configDir string) (Config, error) {
	p := Path(configDir)
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			cfg := Default()
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				return Config{}, err
			}
			if err := Save(configDir, cfg); err != nil {
				return Config{}, err
			}
			return cfg, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}
	// Migration to new image model if needed
	// Ensure Images map is initialized and contains at least discourse
	if cfg.Images == nil || len(cfg.Images) == 0 {
		cfg.Images = map[string]ImageConfig{}
		// Seed from legacy fields
		discourse := ImageConfig{
			Kind:          "discourse",
			Tag:           defaultIfEmpty(cfg.ImageTag, "ai_agent"),
			Workdir:       defaultIfEmpty(cfg.Workdir, "/var/www/discourse"),
			ContainerPort: valueOrDefault(cfg.ContainerPort, 4200),
			Dockerfile:    ImageSource{Source: "stock", StockName: "discourse"},
		}
		cfg.Images["discourse"] = discourse
	}
	if cfg.SelectedImage == "" {
		cfg.SelectedImage = "discourse"
	}
	if cfg.ContainerImages == nil {
		cfg.ContainerImages = map[string]string{}
	}
	if cfg.CopyFiles == nil {
		cfg.CopyFiles = map[string]string{
			"~/.codex/auth.json": "/home/discourse/.codex/auth.json",
		}
	}
	return cfg, nil
}

func Save(configDir string, cfg Config) error {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(configDir), b, 0o644)
}

// Helpers for migration/defaulting
func defaultIfEmpty(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func valueOrDefault(value int, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}
