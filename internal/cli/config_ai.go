package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"dv/internal/ai/discourse"
	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var aiFeatureSettings = []string{
	"ai_sentiment_enabled",
	"ai_helper_enabled",
	"ai_embeddings_enabled",
	"ai_embeddings_per_post_enabled",
	"ai_embeddings_semantic_related_topics_enabled",
	"ai_embeddings_semantic_search_enabled",
	"ai_embeddings_semantic_quick_search_enabled",
	"ai_summarization_enabled",
	"ai_summary_gists_enabled",
	"ai_bot_enabled",
	"ai_discover_enabled",
	"ai_discord_search_enabled",
	"ai_spam_detection_enabled",
	"ai_rag_images_enabled",
	"ai_translation_enabled",
}

var configAICmd = &cobra.Command{
	Use:   "ai",
	Short: "Configure Discourse AI LLMs via a delightful TUI",
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		containerOverride, _ := cmd.Flags().GetString("container")
		containerName := strings.TrimSpace(containerOverride)
		if containerName == "" {
			containerName = currentAgentName(cfg)
		}
		if containerName == "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "No container selected. Run 'dv start' or pass --container.")
			return nil
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
			_, resolved, err := resolveImage(cfg, "")
			if err != nil {
				return err
			}
			imgCfg = resolved
		}
		discourseRoot := strings.TrimSpace(imgCfg.Workdir)
		if discourseRoot == "" {
			discourseRoot = "/var/www/discourse"
		}

		verbose, _ := cmd.Flags().GetBool("verbose")
		client := discourse.NewClient(containerName, discourseRoot, verbose)

		cacheDir, err := xdg.CacheDir()
		if err != nil {
			return err
		}
		providerCache := filepath.Join(cacheDir, "ai_models")
		env := map[string]string{}
		for _, kv := range os.Environ() {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				env[parts[0]] = parts[1]
			}
		}

		model := newAiConfigModel(aiConfigOptions{
			client:       client,
			env:          env,
			container:    containerName,
			discourseDir: discourseRoot,
			ctx:          cmd.Context(),
			loadingState: true,
			cacheDir:     providerCache,
		})

		program := tea.NewProgram(model, tea.WithContext(cmd.Context()))
		if _, runErr := program.Run(); runErr != nil {
			return runErr
		}
		return nil
	},
}

func init() {
	configAICmd.Flags().String("container", "", "Container to configure (defaults to selected agent)")
	configAICmd.Flags().Bool("verbose", false, "Print verbose debugging output")
	configCmd.AddCommand(configAICmd)
}

func truncate(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "...(truncated)"
}
