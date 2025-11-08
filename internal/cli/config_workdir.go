package cli

import (
	"fmt"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/xdg"
)

var configWorkdirCmd = &cobra.Command{
	Use:   "workdir [PATH]",
	Short: "Show or override the default container workdir used by dv run/enter/extract",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		reset, _ := cmd.Flags().GetBool("reset")
		if reset && len(args) > 0 {
			return fmt.Errorf("cannot supply PATH while using --reset")
		}

		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		if reset {
			if strings.TrimSpace(cfg.CustomWorkdir) == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "Workdir override already cleared.")
				return nil
			}
			cfg.CustomWorkdir = ""
			if err := config.Save(configDir, cfg); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Workdir override cleared; runtime commands now use image defaults.")
			return nil
		}

		if len(args) == 0 {
			imgName, imgCfg, err := resolveImage(cfg, "")
			if err != nil {
				return err
			}
			imageWorkdir := strings.TrimSpace(imgCfg.Workdir)
			if imageWorkdir == "" {
				imageWorkdir = "/var/www/discourse"
			}
			effective := config.EffectiveWorkdir(cfg, imgCfg)
			override := strings.TrimSpace(cfg.CustomWorkdir)

			fmt.Fprintf(cmd.OutOrStdout(), "Selected image: %s\n", imgName)
			fmt.Fprintf(cmd.OutOrStdout(), "Image workdir: %s\n", imageWorkdir)
			if override == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "Override: (not set)")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Override: %s\n", override)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Effective workdir: %s\n", effective)
			return nil
		}

		newWorkdir := strings.TrimSpace(args[0])
		if !strings.HasPrefix(newWorkdir, "/") {
			return fmt.Errorf("workdir must be an absolute path inside the container")
		}
		cleaned := path.Clean(newWorkdir)

		cfg.CustomWorkdir = cleaned
		if err := config.Save(configDir, cfg); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Workdir override set to %s\n", cleaned)
		fmt.Fprintln(cmd.OutOrStdout(), "Future 'dv run', 'dv enter', 'dv run-agent', and 'dv extract' commands will use this path.")
		return nil
	},
}

func init() {
	configWorkdirCmd.Flags().Bool("reset", false, "Remove the override and fall back to the image workdir")
	configCmd.AddCommand(configWorkdirCmd)
}
