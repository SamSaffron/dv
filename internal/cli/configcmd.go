package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/xdg"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage dv configuration",
}

var configGetCmd = &cobra.Command{
	Use:   "get KEY",
	Short: "Get a config value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}
		key := args[0]
		val, err := getConfigField(cfg, key)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), val)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set KEY VALUE",
	Short: "Set a config value",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}
		key, value := args[0], args[1]
		if err := setConfigField(&cfg, key, value); err != nil {
			return err
		}
		return config.Save(configDir, cfg)
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show full config JSON",
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
		return nil
	},
}

func init() {
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configShowCmd)
}

func getConfigField(cfg config.Config, key string) (string, error) {
	switch key {
	case "imageTag":
		return cfg.ImageTag, nil
	case "defaultContainerName":
		return cfg.DefaultContainer, nil
	case "workdir":
		return cfg.Workdir, nil
	case "hostStartingPort":
		return fmt.Sprint(cfg.HostStartingPort), nil
	case "containerPort":
		return fmt.Sprint(cfg.ContainerPort), nil
	case "selectedAgent":
		return cfg.SelectedAgent, nil
	case "discourseRepo":
		return cfg.DiscourseRepo, nil
	case "extractBranchPrefix":
		return cfg.ExtractBranchPrefix, nil
	default:
		return "", fmt.Errorf("unknown key: %s", key)
	}
}

func setConfigField(cfg *config.Config, key, val string) error {
	switch key {
	case "imageTag":
		cfg.ImageTag = val
	case "defaultContainerName":
		cfg.DefaultContainer = val
	case "workdir":
		cfg.Workdir = val
	case "hostStartingPort":
		var v int
		_, err := fmt.Sscanf(val, "%d", &v)
		if err != nil {
			return err
		}
		cfg.HostStartingPort = v
	case "containerPort":
		var v int
		_, err := fmt.Sscanf(val, "%d", &v)
		if err != nil {
			return err
		}
		cfg.ContainerPort = v
	case "selectedAgent":
		cfg.SelectedAgent = val
	case "discourseRepo":
		cfg.DiscourseRepo = val
	case "extractBranchPrefix":
		cfg.ExtractBranchPrefix = val
	default:
		return fmt.Errorf("unknown key: %s", key)
	}
	return nil
}
