package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/xdg"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List containers created from the selected image",
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		imgName, imgCfg, err := resolveImage(cfg, "")
		if err != nil {
			return err
		}

		out, _ := runShell("docker ps -a --format '{{.Names}}\t{{.Image}}\t{{.Status}}'")
		selected := cfg.SelectedAgent
		printed := false
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) < 3 {
				continue
			}
			name, image, status := parts[0], parts[1], parts[2]
			if image != imgCfg.Tag {
				continue
			}
			mark := " "
			if selected != "" && name == selected {
				mark = "*"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\t%s\n", mark, name, status)
			printed = true
		}
		if !printed {
			fmt.Fprintf(cmd.OutOrStdout(), "(no agents found for image '%s')\n", imgCfg.Tag)
		}
		if selected != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Selected: %s\n", selected)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Selected: (none)")
		}
		_ = imgName // not printed but kept for clarity
		return nil
	},
}
