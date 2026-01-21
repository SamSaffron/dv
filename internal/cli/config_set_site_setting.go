package cli

import (
	"dv/internal/config"
	"dv/internal/discourse"
	"dv/internal/xdg"
	"fmt"
	"github.com/spf13/cobra"
	"strings"
)

var setSiteSettingCommand = &cobra.Command{
	Use:   "set-site-setting [setting] [value]",
	Short: "Set an individual site setting value",
	Args:  cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		setting := args[0]
		value := args[1]

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

		fmt.Fprintf(cmd.OutOrStdout(), "Setting site setting '%s' to '%s'...\n", setting, value)

		client, err := discourse.NewClientWrapper(containerName, cfg, false)
		if err != nil {
			return fmt.Errorf("create discourse client: %w", err)
		}
		if err := client.EnsureAPIKey(); err != nil {
			return fmt.Errorf("ensure API key: %w", err)
		}

		if err := client.SetSiteSetting(setting, value); err != nil {
			return fmt.Errorf("error setting site setting: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Change was successful!\n")
		return nil
	},
}

func init() {
	setSiteSettingCommand.Flags().String("setting", "", "Site setting name")
	setSiteSettingCommand.Flags().String("value", "", "Site setting value")
	setSiteSettingCommand.Flags().String("container", "", "Target container (defaults to selected agent)")
	configCmd.AddCommand(setSiteSettingCommand)
}
