package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type Config struct {
	ImageTag         string            `json:"imageTag"`
	DefaultContainer string            `json:"defaultContainerName"`
	Workdir          string            `json:"workdir"`
	CustomWorkdir    string            `json:"customWorkdir,omitempty"`
	CustomWorkdirs   map[string]string `json:"customWorkdirs,omitempty"`
	LocalProxy       LocalProxyConfig  `json:"localProxy,omitempty"`
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
	CopyFiles map[string]string `json:"copyFiles,omitempty"`
	// CopyRules is the preferred representation of copy mappings with optional
	// agent scoping.
	CopyRules []CopyRule `json:"copyRules,omitempty"`
}

// CopyRule represents a host->container copy mapping with optional agent scoping.
type CopyRule struct {
	Host      string   `json:"host"`
	Container string   `json:"container"`
	Agents    []string `json:"agents,omitempty"`
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

type LocalProxyConfig struct {
	Enabled       bool   `json:"enabled"`
	ContainerName string `json:"containerName"`
	ImageTag      string `json:"imageTag"`
	HTTPPort      int    `json:"httpPort"`
	APIPort       int    `json:"apiPort"`
	Public        bool   `json:"public"`
}

func Default() Config {
	return Config{
		ImageTag:         "ai_agent",
		DefaultContainer: "ai_agent",
		Workdir:          "/var/www/discourse",
		CustomWorkdirs:   map[string]string{},
		LocalProxy:       defaultLocalProxyConfig(),
		HostStartingPort: 4200,
		ContainerPort:    4200,
		EnvPassthrough: []string{
			"CURSOR_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
			"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
			"CLAUDE_CODE_USE_BEDROCK", "DEEPSEEK_API_KEY", "GEMINI_API_KEY",
			"AMP_API_KEY", "GH_TOKEN", "OPENROUTER_API_KEY", "KILOCODE_API_KEY",
			"FACTORY_API_KEY", "MISTRAL_API_KEY",
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
		CopyRules:       defaultCopyRules(),
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
	if cfg.CustomWorkdirs == nil {
		cfg.CustomWorkdirs = map[string]string{}
	}
	cfg.migrateCopyFiles()
	if w := strings.TrimSpace(cfg.CustomWorkdir); w != "" {
		target := cfg.SelectedAgent
		if target == "" {
			target = cfg.DefaultContainer
		}
		if target == "" {
			target = "default"
		}
		cfg.CustomWorkdirs[target] = w
		cfg.CustomWorkdir = ""
	}
	cfg.LocalProxy.ApplyDefaults()
	return cfg, nil
}

func Save(configDir string, cfg Config) error {
	cfg.migrateCopyFiles()
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

func defaultCopyRules() []CopyRule {
	return []CopyRule{
		{
			Host:      "~/.codex/auth.json",
			Container: "/home/discourse/.codex/auth.json",
			Agents:    []string{"codex"},
		},
		{
			Host:      "~/.kilocode/config.json",
			Container: "/home/discourse/.kilocode/config.json",
			Agents:    []string{"kilocode"},
		},
		{
			Host:      "~/.gemini/GEMINI.md",
			Container: "/home/discourse/.gemini/GEMINI.md",
			Agents:    []string{"gemini"},
		},
		{
			Host:      "~/.gemini/*.json",
			Container: "/home/discourse/.gemini/",
			Agents:    []string{"gemini"},
		},
		{
			Host:      "~/.gemini/google_account_id",
			Container: "/home/discourse/.gemini/",
			Agents:    []string{"gemini"},
		},
	}
}

func (cfg *Config) migrateCopyFiles() {
	origNil := cfg.CopyRules == nil
	if origNil {
		cfg.CopyRules = []CopyRule{}
	}
	migrated := false
	if len(cfg.CopyRules) == 0 && len(cfg.CopyFiles) > 0 {
		keys := make([]string, 0, len(cfg.CopyFiles))
		for src := range cfg.CopyFiles {
			keys = append(keys, src)
		}
		sort.Strings(keys)
		for _, src := range keys {
			cfg.CopyRules = append(cfg.CopyRules, CopyRule{
				Host:      src,
				Container: cfg.CopyFiles[src],
			})
		}
		migrated = true
	}
	switch {
	case origNil:
		cfg.CopyRules = appendMissingDefaultCopyRules(cfg.CopyRules, defaultCopyRules())
	case migrated:
		cfg.CopyRules = appendMissingDefaultCopyRules(cfg.CopyRules, defaultCopyRules())
	}
	cfg.CopyFiles = nil
}

func appendMissingDefaultCopyRules(rules []CopyRule, defaults []CopyRule) []CopyRule {
	existing := map[string]struct{}{}
	for _, r := range rules {
		key := strings.ToLower(strings.TrimSpace(r.Host)) + "\x00" + strings.ToLower(strings.TrimSpace(r.Container))
		existing[key] = struct{}{}
	}
	for _, d := range defaults {
		key := strings.ToLower(strings.TrimSpace(d.Host)) + "\x00" + strings.ToLower(strings.TrimSpace(d.Container))
		if _, ok := existing[key]; ok {
			continue
		}
		rules = append(rules, d)
	}
	return rules
}

// EffectiveWorkdir returns the runtime working directory dv commands should use.
// Priority:
//  1. Container-specific override set via `dv config workdir`
//  2. Per-image workdir
//  3. Legacy global workdir field
//  4. Default /var/www/discourse
func EffectiveWorkdir(cfg Config, img ImageConfig, containerName string) string {
	if containerName != "" {
		if cfg.CustomWorkdirs != nil {
			if w := strings.TrimSpace(cfg.CustomWorkdirs[containerName]); w != "" {
				return path.Clean(w)
			}
		}
	}
	if w := strings.TrimSpace(img.Workdir); w != "" {
		return w
	}
	if w := strings.TrimSpace(cfg.Workdir); w != "" {
		return w
	}
	return "/var/www/discourse"
}

func defaultLocalProxyConfig() LocalProxyConfig {
	return LocalProxyConfig{
		ContainerName: "dv-local-proxy",
		ImageTag:      "dv-local-proxy",
		HTTPPort:      80,
		APIPort:       2080,
		Public:        false,
	}
}

func (c *LocalProxyConfig) ApplyDefaults() {
	defaults := defaultLocalProxyConfig()
	if strings.TrimSpace(c.ContainerName) == "" {
		c.ContainerName = defaults.ContainerName
	}
	if strings.TrimSpace(c.ImageTag) == "" {
		c.ImageTag = defaults.ImageTag
	}
	if c.HTTPPort == 0 {
		c.HTTPPort = defaults.HTTPPort
	}
	if c.APIPort == 0 {
		c.APIPort = defaults.APIPort
	}
	// Public flag defaults to false (private binding)
	if !c.Public {
		c.Public = defaults.Public
	}
}
