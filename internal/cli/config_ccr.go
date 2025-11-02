package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/openrouter"
	"dv/internal/xdg"
)

const (
	iconFolder  = "\U000f028b" // nf-md-folder_outline
	iconGit     = "\ue0a0"     // nf-dev-git_branch
	iconRobot   = "\U000f06a9" // nf-md-robot_outline
	iconArrowUp = "\u2191"     // up arrow
	iconArrowDn = "\u2193"     // down arrow
)

// Router presets for different use cases
type routerPreset struct {
	name        string
	description string
	defaultM    string
	background  string
	think       string
	longContext string
	webSearch   string
	lowLatency  string
}

var routerPresets = []routerPreset{
	{
		name:        "Balanced (Recommended)",
		description: "Mix of capable models for general development",
		defaultM:    "anthropic/claude-sonnet-4.5",
		background:  "x-ai/grok-code-fast-1",
		think:       "anthropic/claude-sonnet-4.5",
		longContext: "anthropic/claude-sonnet-4.5",
		webSearch:   "x-ai/grok-code-fast-1",
		lowLatency:  "x-ai/grok-code-fast-1",
	},
	{
		name:        "Premium Performance",
		description: "Best paid models for maximum quality",
		defaultM:    "anthropic/claude-sonnet-4.5",
		background:  "anthropic/claude-sonnet-4.5",
		think:       "anthropic/claude-sonnet-4.5",
		longContext: "anthropic/claude-sonnet-4.5",
		webSearch:   "anthropic/claude-sonnet-4.5",
		lowLatency:  "anthropic/claude-sonnet-4.5",
	},
	{
		name:        "Budget Conscious",
		description: "Cheap paid models with free tier fallbacks",
		defaultM:    "x-ai/grok-code-fast-1",
		background:  "minimax/minimax-m2:free",
		think:       "x-ai/grok-code-fast-1",
		longContext: "x-ai/grok-code-fast-1",
		webSearch:   "minimax/minimax-m2:free",
		lowLatency:  "x-ai/grok-code-fast-1",
	},
	{
		name:        "Free Tier Only",
		description: "100% free models (no API costs)",
		defaultM:    "minimax/minimax-m2:free",
		background:  "minimax/minimax-m2:free",
		think:       "minimax/minimax-m2:free",
		longContext: "minimax/minimax-m2:free",
		webSearch:   "minimax/minimax-m2:free",
		lowLatency:  "minimax/minimax-m2:free",
	},
	{
		name:        "Custom",
		description: "Configure each route individually",
		defaultM:    "", // signals custom mode
	},
}

var (
	defaultFreeModels = []string{
		"minimax/minimax-m2:free",
		"nvidia/nemotron-nano-12b-v2-vl:free",
		"alibaba/tongyi-deepresearch-30b-a3b:free",
		"deepseek/deepseek-r1-0528-qwen3-8b:free",
		"z-ai/glm-4.5-air:free",
		"mistralai/mistral-small-3.2-24b-instruct:free",
		"moonshotai/kimi-k2:free",
		"qwen/qwen3-coder:free",
		"deepseek/deepseek-chat-v3.1:free",
		"openai/gpt-oss-20b:free",
		"qwen/qwen2.5-coder:free",
		"mistralai/mistral-nemo-mini-7b-instruct:free",
	}
	defaultPaidModels = []string{
		"x-ai/grok-code-fast-1",
		"anthropic/claude-4.5-sonnet-20250929",
		"minimax/minimax-m2",
		"anthropic/claude-4-sonnet-20250522",
		"qwen/qwen3-coder-480b-a35b-07-25",
		"z-ai/glm-4.6",
		"google/gemini-2.5-flash",
		"openai/gpt-5-2025-08-07",
		"anthropic/claude-4.5-haiku-20251001",
		"google/gemini-2.5-pro",
		"deepseek/deepseek-r1",
		"meta-llama/llama-4.1-70b-instruct",
	}
)

var configCcrCmd = &cobra.Command{
	Use:   "ccr",
	Short: "Bootstrap Claude Code Router with popular OpenRouter models",
	RunE: func(cmd *cobra.Command, args []string) error {
		freeCount, _ := cmd.Flags().GetInt("free-count")
		paidCount, _ := cmd.Flags().GetInt("paid-count")
		sourceURL, _ := cmd.Flags().GetString("source-url")
		category, _ := cmd.Flags().GetString("category")
		period, _ := cmd.Flags().GetString("period")
		cacheTTL, _ := cmd.Flags().GetDuration("cache-ttl")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		skipModelEval, _ := cmd.Flags().GetBool("skip-model-eval")
		resetCache, _ := cmd.Flags().GetBool("reset-cache")

		cacheDir, err := xdg.CacheDir()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return err
		}

		if resetCache {
			if err := openrouter.ResetCache(cacheDir); err != nil {
				return fmt.Errorf("reset cache: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Cleared cached OpenRouter rankings.")
			return nil
		}

		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		containerName, _ := cmd.Flags().GetString("name")
		if containerName == "" {
			containerName = currentAgentName(cfg)
		}
		containerName = strings.TrimSpace(containerName)
		if containerName == "" {
			return fmt.Errorf("no container selected; run 'dv start' or use --name")
		}

		if !docker.Exists(containerName) {
			fmt.Fprintf(cmd.OutOrStdout(), "Container '%s' does not exist. Run 'dv start' first.\n", containerName)
			return nil
		}
		if !docker.Running(containerName) {
			fmt.Fprintf(cmd.OutOrStdout(), "Starting container '%s'...\n", containerName)
			if err := docker.Start(containerName); err != nil {
				return err
			}
		}

		imgName := cfg.ContainerImages[containerName]
		var imgCfg config.ImageConfig
		if imgName != "" {
			imgCfg = cfg.Images[imgName]
		} else {
			_, imgCfg, err = resolveImage(cfg, "")
			if err != nil {
				return err
			}
		}
		workdir := imgCfg.Workdir

		// Ensure CCR binary exists
		if _, err := docker.ExecOutput(containerName, workdir, []string{"bash", "-lc", "command -v ccr"}); err != nil {
			return fmt.Errorf("ccr CLI not found in container '%s'; install it with `npm install -g @musistudio/claude-code-router` inside the container", containerName)
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
		defer cancel()

		trending, trendErr := openrouter.FetchTrending(ctx, openrouter.Options{
			CacheDir:  cacheDir,
			CacheTTL:  cacheTTL,
			FreeCount: freeCount,
			PaidCount: paidCount,
			SourceURL: sourceURL,
			Category:  category,
			Period:    period,
			UserAgent: "dv/ccr-config (+https://github.com/discourse/dv)",
		})

		containerConfigPath := "/home/discourse/.claude-code-router/config.json"

		freeModels := append([]string{}, defaultFreeModels...)
		paidModels := append([]string{}, defaultPaidModels...)
		notes := []string{}

		if trendErr == nil {
			freeModels = trending.Free
			paidModels = trending.Paid
			if trending.FromCache {
				notes = append(notes, fmt.Sprintf("used cached rankings from %s (%s)", trending.Source, trending.RetrievedAt.Format(time.RFC3339)))
			} else {
				notes = append(notes, fmt.Sprintf("fetched rankings from %s at %s", trending.Source, trending.RetrievedAt.Format(time.RFC3339)))
			}
		} else if !errors.Is(trendErr, context.DeadlineExceeded) {
			notes = append(notes, fmt.Sprintf("ranking fetch failed (%v); using built-in defaults", trendErr))
		} else {
			notes = append(notes, "ranking fetch timed out; using built-in defaults")
		}

		var defaultPadFree, defaultPadPaid bool
		freeModels, defaultPadFree = ensureCapacity(freeModels, defaultFreeModels, freeCount)
		paidModels, defaultPadPaid = ensureCapacity(paidModels, defaultPaidModels, paidCount)
		if defaultPadFree {
			notes = append(notes, "extended free-tier list with built-in defaults to reach requested count")
		}
		if defaultPadPaid {
			notes = append(notes, "extended paid list with built-in defaults to reach requested count")
		}

		if len(freeModels) > freeCount {
			freeModels = freeModels[:freeCount]
		}
		if len(paidModels) > paidCount {
			paidModels = paidModels[:paidCount]
		}

		printModelSet(cmd, "Free-tier models", freeModels)
		printModelSet(cmd, "Paid-tier models", paidModels)

		if len(notes) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "\nNotes:")
			for _, msg := range notes {
				fmt.Fprintf(cmd.OutOrStdout(), "- %s\n", msg)
			}
		}

		meta := map[string]interface{}{
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"free_count":   len(freeModels),
			"paid_count":   len(paidModels),
			"base":         "generated",
		}
		if trendErr == nil {
			meta["openrouter_source"] = trending.Source
			meta["openrouter_retrieved_at"] = trending.RetrievedAt.Format(time.RFC3339)
			meta["openrouter_cache"] = trending.FromCache
		}
		if len(notes) > 0 {
			meta["notes"] = notes
		}

		// Combine all models into a single provider
		allModels := append([]string{}, paidModels...)
		allModels = append(allModels, freeModels...)

		// Select router preset
		if !dryRun && !skipModelEval {
			fmt.Fprintln(cmd.OutOrStdout(), "")
			selectedPreset, err := selectRouterPreset()
			if err != nil {
				return fmt.Errorf("preset selection: %w", err)
			}

			// Handle custom mode - show current config and allow editing
			if selectedPreset.defaultM == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "\nCustom configuration mode...")

				// Try to read existing config
				existingRouter, err := readExistingRouter(containerName, workdir, containerConfigPath)
				if err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "No existing config found, starting fresh.\n")
				}

				// Launch custom router editor
				customRouter, err := selectCustomRouter(allModels, existingRouter)
				if err != nil {
					return fmt.Errorf("custom router selection: %w", err)
				}

				ccrCfg := map[string]interface{}{
					"LOG":                  true,
					"NON_INTERACTIVE_MODE": false,
					"API_TIMEOUT_MS":       600000,
					"Providers": []map[string]interface{}{
						makeProvider("openrouter", allModels),
					},
					"Router":     customRouter,
					"StatusLine": makeStatusLineConfig(),
					"_dv":        meta,
				}

				return writeConfigToContainer(ccrCfg, containerName, workdir, containerConfigPath)
			}

			// Use selected preset
			fmt.Fprintf(cmd.OutOrStdout(), "\n? Selected: %s\n", selectedPreset.name)

			ccrCfg := map[string]interface{}{
				"LOG":                  true,
				"NON_INTERACTIVE_MODE": false,
				"API_TIMEOUT_MS":       600000,
				"Providers": []map[string]interface{}{
					makeProvider("openrouter", allModels),
				},
				"Router": map[string]string{
					"default":     "openrouter," + selectedPreset.defaultM,
					"background":  "openrouter," + selectedPreset.background,
					"think":       "openrouter," + selectedPreset.think,
					"longContext": "openrouter," + selectedPreset.longContext,
					"webSearch":   "openrouter," + selectedPreset.webSearch,
					"lowLatency":  "openrouter," + selectedPreset.lowLatency,
				},
				"StatusLine": makeStatusLineConfig(),
				"_dv":        meta,
			}

			return writeConfigToContainer(ccrCfg, containerName, workdir, containerConfigPath)
		}

		// Dry-run or skip-model-eval: use default routing
		ccrCfg := map[string]interface{}{
			"LOG":                  true,
			"NON_INTERACTIVE_MODE": false,
			"API_TIMEOUT_MS":       600000,
			"Providers": []map[string]interface{}{
				makeProvider("openrouter", allModels),
			},
			"Router": map[string]string{
				"default":     joinRouter("openrouter", paidModels, "anthropic/claude-4.5-sonnet-20250929"),
				"background":  joinRouter("openrouter", freeModels, "minimax/minimax-m2:free"),
				"think":       joinRouter("openrouter", paidModels, "x-ai/grok-code-fast-1"),
				"longContext": joinRouter("openrouter", paidModels, "qwen/qwen3-coder-480b-a35b-07-25"),
				"webSearch":   joinRouter("openrouter", freeModels, "deepseek/deepseek-r1-0528-qwen3-8b:free"),
				"lowLatency":  joinRouter("openrouter", paidModels, "google/gemini-2.5-flash"),
			},
			"StatusLine": makeStatusLineConfig(),
			"_dv":        meta,
		}

		b, err := json.MarshalIndent(ccrCfg, "", "  ")
		if err != nil {
			return err
		}

		if dryRun {
			fmt.Fprintln(cmd.OutOrStdout(), "\nDry run (no container changes):")
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		}

		tmpDir, err := os.MkdirTemp("", "dv-ccr-config-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)

		updatedPath := filepath.Join(tmpDir, "config.json")
		if err := os.WriteFile(updatedPath, append(b, '\n'), 0o644); err != nil {
			return err
		}

		if _, err := docker.ExecOutput(containerName, workdir, []string{"bash", "-lc", "mkdir -p ~/.claude-code-router"}); err != nil {
			return err
		}
		if err := docker.CopyToContainer(containerName, updatedPath, containerConfigPath); err != nil {
			return fmt.Errorf("failed to copy CCR config into container: %w", err)
		}
		if _, err := docker.ExecAsRoot(containerName, workdir, []string{"chown", "discourse:discourse", containerConfigPath}); err != nil {
			return err
		}

		fmt.Fprintln(cmd.OutOrStdout(), "\nCreated new CCR config with OpenRouter model presets.")

		if skipModelEval {
			return nil
		}

		envs := make([]string, 0, 3)
		if _, ok := os.LookupEnv("TERM"); ok {
			envs = append(envs, "TERM")
		}
		if _, ok := os.LookupEnv("OPENROUTER_API_KEY"); ok {
			envs = append(envs, "OPENROUTER_API_KEY")
		} else {
			fmt.Fprintln(cmd.ErrOrStderr(), "Warning: OPENROUTER_API_KEY is not set on host; CCR commands may fail to authenticate.")
		}
		if _, ok := os.LookupEnv("OPENROUTER_KEY"); ok {
			envs = append(envs, "OPENROUTER_KEY")
		}

		fmt.Fprintln(cmd.OutOrStdout(), "\nLaunching `ccr model` inside the container so you can fine-tune model ordering (press Ctrl+C to exit)...")
		return docker.ExecInteractive(containerName, workdir, envs, []string{"bash", "-lc", "ccr model"})
	},
}

func init() {
	configCcrCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
	configCcrCmd.Flags().Int("free-count", 10, "Number of free-tier OpenRouter models to include")
	configCcrCmd.Flags().Int("paid-count", 10, "Number of paid OpenRouter models to include")
	configCcrCmd.Flags().String("source-url", "", "Override OpenRouter rankings source URL")
	configCcrCmd.Flags().String("category", "", "Optional OpenRouter category (e.g. programming)")
	configCcrCmd.Flags().String("period", "week", "Ranking period (hour, day, week, month)")
	configCcrCmd.Flags().Duration("cache-ttl", 6*time.Hour, "How long to reuse cached rankings")
	configCcrCmd.Flags().Bool("dry-run", false, "Print generated config without touching the container")
	configCcrCmd.Flags().Bool("skip-model-eval", false, "Skip launching `ccr model` after writing config")
	configCcrCmd.Flags().Bool("reset-cache", false, "Delete cached OpenRouter rankings before fetching")
	configCmd.AddCommand(configCcrCmd)
}

func ensureCapacity(current []string, fallback []string, want int) ([]string, bool) {
	if want <= 0 {
		return current, false
	}
	if len(current) >= want {
		return current, false
	}
	out := append([]string{}, current...)
	added := false
	for _, candidate := range fallback {
		if len(out) >= want {
			break
		}
		found := false
		for _, existing := range out {
			if existing == candidate {
				found = true
				break
			}
		}
		if !found {
			out = append(out, candidate)
			added = true
		}
	}
	return out, added
}

func makeProvider(name string, models []string) map[string]interface{} {
	return map[string]interface{}{
		"name":         name,
		"api_base_url": "https://openrouter.ai/api/v1/chat/completions",
		"api_key":      "$OPENROUTER_API_KEY",
		"models":       models,
		"transformer": map[string]interface{}{
			"use": []string{"openrouter"},
		},
	}
}

func printModelSet(cmd *cobra.Command, title string, models []string) {
	fmt.Fprintf(cmd.OutOrStdout(), "\n%s (%d):\n", title, len(models))
	for i, m := range models {
		fmt.Fprintf(cmd.OutOrStdout(), "  %2d. %s\n", i+1, m)
	}
}

func joinRouter(provider string, models []string, fallback string) string {
	if len(models) == 0 {
		return provider + "," + fallback
	}
	return provider + "," + models[0]
}

func makeStatusLineConfig() map[string]interface{} {
	return map[string]interface{}{
		"enabled":      true,
		"currentStyle": "default",
		"default": map[string]interface{}{
			"modules": []map[string]interface{}{
				{"type": "workDir", "icon": iconFolder, "text": "{{workDirName}}", "color": "bright_blue"},
				{"type": "gitBranch", "icon": iconGit, "text": "{{gitBranch}}", "color": "bright_magenta"},
				{"type": "model", "icon": iconRobot, "text": "{{model}}", "color": "bright_cyan"},
				{"type": "usage", "icon": iconArrowUp, "text": "{{inputTokens}}", "color": "bright_green"},
				{"type": "usage", "icon": iconArrowDn, "text": "{{outputTokens}}", "color": "bright_yellow"},
			},
		},
		"powerline": map[string]interface{}{
			"modules": []map[string]interface{}{
				{"type": "workDir", "icon": iconFolder, "text": "{{workDirName}}", "color": "white", "background": "bg_bright_blue"},
				{"type": "gitBranch", "icon": iconGit, "text": "{{gitBranch}}", "color": "white", "background": "bg_bright_magenta"},
				{"type": "model", "icon": iconRobot, "text": "{{model}}", "color": "white", "background": "bg_bright_cyan"},
				{"type": "usage", "icon": iconArrowUp, "text": "{{inputTokens}}", "color": "white", "background": "bg_bright_green"},
				{"type": "usage", "icon": iconArrowDn, "text": "{{outputTokens}}", "color": "white", "background": "bg_bright_yellow"},
			},
		},
	}
}

func writeConfigToContainer(ccrCfg map[string]interface{}, containerName, workdir, containerConfigPath string) error {
	b, err := json.MarshalIndent(ccrCfg, "", "  ")
	if err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "dv-ccr-config-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	updatedPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(updatedPath, append(b, '\n'), 0o644); err != nil {
		return err
	}

	if _, err := docker.ExecOutput(containerName, workdir, []string{"bash", "-lc", "mkdir -p ~/.claude-code-router"}); err != nil {
		return err
	}
	if err := docker.CopyToContainer(containerName, updatedPath, containerConfigPath); err != nil {
		return fmt.Errorf("failed to copy CCR config into container: %w", err)
	}
	if _, err := docker.ExecAsRoot(containerName, workdir, []string{"chown", "discourse:discourse", containerConfigPath}); err != nil {
		return err
	}

	fmt.Println("\n✓ CCR config written to container")
	return nil
}
