package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var newCmd = &cobra.Command{
	Use:   "new [NAME]",
	Short: "Create a new agent for the selected image and select it",
	Args:  cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		imageOverride, _ := cmd.Flags().GetString("image")
		name := ""
		if len(args) == 1 {
			name = args[0]
		} else {
			name = autogenName()
		}
		if docker.Exists(name) {
			return fmt.Errorf("an agent named '%s' already exists", name)
		}
		cfg.SelectedAgent = name

		// Determine which image to use
		imgName, imgCfg, err := resolveImage(cfg, imageOverride)
		if err != nil {
			return err
		}
		imageTag := imgCfg.Tag
		workdir := imgCfg.Workdir

		if err := config.Save(configDir, cfg); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Creating agent '%s' from image '%s'...\n", name, imageTag)
		// initialize container by running a no-op command
		if err := ensureContainerRunningWithWorkdir(cmd, cfg, name, workdir, imageTag, imgName, false); err != nil {
			return err
		}
		if cfg.ContainerImages == nil {
			cfg.ContainerImages = map[string]string{}
		}
		cfg.ContainerImages[name] = imgName
		_ = config.Save(configDir, cfg)
		fmt.Fprintf(cmd.OutOrStdout(), "Agent '%s' is ready and selected.\n", name)
		return nil
	},
}

func init() {
	newCmd.Flags().String("image", "", "Image to use (defaults to selected image)")
}
