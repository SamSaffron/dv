package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var extractPluginCmd = &cobra.Command{
	Use:   "plugin [name]",
	Short: "Extract changes from a plugin inside the container",
	Args:  cobra.ExactArgs(1),
	// Provide dynamic completion for plugin names that are separate repos
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Only complete the first argument
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		configDir, err := xdg.ConfigDir()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		name := currentAgentName(cfg)
		// If not running, we cannot inspect; return no suggestions to avoid side effects
		if !docker.Running(name) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		// Resolve workdir
		imgName := cfg.ContainerImages[name]
		_, imgCfg, err := resolveImage(cfg, imgName)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		work := imgCfg.Workdir
		// List plugin directories that are their own git repos (i.e., not part of core repo)
		script := `
set +e
core=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
for d in plugins/*; do
  [ -d "$d" ] || continue
  if git -C "$d" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    tl=$(git -C "$d" rev-parse --show-toplevel 2>/dev/null)
    if [ "$tl" != "$core" ] && [ -n "$tl" ]; then
      b=$(basename "$d")
      echo "$b"
    fi
  fi
done
`
		out, err := docker.ExecOutput(name, work, nil, []string{"bash", "-lc", script})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var suggestions []string
		prefix := strings.ToLower(strings.TrimSpace(toComplete))
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			p := strings.TrimSpace(line)
			if p == "" {
				continue
			}
			if prefix == "" || strings.HasPrefix(strings.ToLower(p), prefix) {
				suggestions = append(suggestions, p)
			}
		}
		return suggestions, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		pluginName := strings.TrimSpace(args[0])
		if pluginName == "" {
			return fmt.Errorf("plugin name is required")
		}

		// Flags controlling post-extract behavior and output
		chdir, _ := cmd.Flags().GetBool("chdir")
		echoCd, _ := cmd.Flags().GetBool("echo-cd")
		syncMode, _ := cmd.Flags().GetBool("sync")
		syncDebug, _ := cmd.Flags().GetBool("debug")
		customDir, _ := cmd.Flags().GetString("dir")

		if syncMode && chdir {
			return fmt.Errorf("--sync cannot be combined with --chdir")
		}
		if syncMode && echoCd {
			return fmt.Errorf("--sync cannot be combined with --echo-cd")
		}

		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		dataDir, err := xdg.DataDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		name, _ := cmd.Flags().GetString("name")
		if name == "" {
			name = currentAgentName(cfg)
		}

		if !docker.Running(name) {
			return fmt.Errorf("container '%s' is not running; run 'dv start' first", name)
		}

		// Determine image associated with this container, falling back to selected image
		imgName := cfg.ContainerImages[name]
		_, imgCfg, err := resolveImage(cfg, imgName)
		if err != nil {
			return err
		}
		work := imgCfg.Workdir

		// Verify plugin directory exists
		pluginRel := filepath.Join("plugins", pluginName)
		existsOut, err := docker.ExecOutput(name, work, nil, []string{"bash", "-lc", fmt.Sprintf("[ -d %q ] && echo OK || echo MISSING", pluginRel)})
		if err != nil || !strings.Contains(existsOut, "OK") {
			return fmt.Errorf("plugin '%s' not found in %s", pluginName, filepath.Join(work, "plugins"))
		}

		pluginWork := filepath.Join(work, "plugins", pluginName)

		localRepo := filepath.Join(dataDir, fmt.Sprintf("%s_src", pluginName))
		if customDir != "" {
			localRepo = customDir
		}
		display := fmt.Sprintf("plugin %s", pluginName)
		return extractWorkspaceRepo(workspaceExtractOptions{
			cmd:              cmd,
			containerName:    name,
			containerWorkdir: pluginWork,
			localRepo:        localRepo,
			branchName:       pluginName,
			displayName:      display,
			chdir:            chdir,
			echoCd:           echoCd,
			syncMode:         syncMode,
			syncDebug:        syncDebug,
		})
	},
}

func init() {
	extractPluginCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
	extractPluginCmd.Flags().String("dir", "", "Extract to a specific directory instead of default location")
	extractPluginCmd.Flags().Bool("chdir", false, "Open a subshell in the extracted repo directory after completion")
	extractPluginCmd.Flags().Bool("echo-cd", false, "Print 'cd <path>' suitable for eval; suppress other output")
	extractPluginCmd.Flags().Bool("sync", false, "Watch for changes and synchronize container ↔ host")
	extractPluginCmd.Flags().Bool("debug", false, "Verbose logging for sync mode")
	extractCmd.AddCommand(extractPluginCmd)
}
