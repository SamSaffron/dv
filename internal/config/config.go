package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type Config struct {
	ImageTag            string   `json:"imageTag"`
	DefaultContainer    string   `json:"defaultContainerName"`
	Workdir             string   `json:"workdir"`
	HostPort            int      `json:"hostPort"`
	ContainerPort       int      `json:"containerPort"`
	SelectedAgent       string   `json:"selectedAgent"`
	EnvPassthrough      []string `json:"envPassthrough"`
	DiscourseRepo       string   `json:"discourseRepo"`
	ExtractBranchPrefix string   `json:"extractBranchPrefix"`
}

func Default() Config {
	return Config{
		ImageTag:         "ai_agent",
		DefaultContainer: "ai_agent",
		Workdir:          "/var/www/discourse",
		HostPort:         4201,
		ContainerPort:    4200,
		EnvPassthrough: []string{
			"CURSOR_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
			"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
			"CLAUDE_CODE_USE_BEDROCK", "DEEPSEEK_API_KEY", "GEMINI_API_KEY",
		},
		DiscourseRepo:       "https://github.com/discourse/discourse.git",
		ExtractBranchPrefix: "agent-changes",
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
