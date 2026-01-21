package cli

import (
	"dv/internal/config"
	"dv/internal/discourse"
	"dv/internal/xdg"
	"fmt"
	"github.com/spf13/cobra"
	"strings"
)

var getSiteSettingCommand = &cobra.Command{
	Use:   "get-site-setting [setting]",
	Short: "Get an individual site setting value",
	Args:  cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		setting := args[0]

		// Load config and resolve container
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
			return fmt.Errorf("no container selected; run 'dv start' or pass --container")
		}

		client, err := discourse.NewClientWrapper(containerName, cfg, false)
		if err != nil {
			return fmt.Errorf("create discourse client: %w", err)
		}
		if err := client.EnsureAPIKey(); err != nil {
			return fmt.Errorf("ensure API key: %w", err)
		}

		currentValue, err := client.GetSiteSetting(setting)
		if err != nil {
			return fmt.Errorf("error getting site setting: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Current value for '%s' is '%s'\n", setting, currentValue)
		return nil
	},
}

func init() {
	getSiteSettingCommand.Flags().String("setting", "", "Site setting name")
	getSiteSettingCommand.Flags().String("container", "", "Target container (defaults to selected agent)")
	configCmd.AddCommand(getSiteSettingCommand)
}
