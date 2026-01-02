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

var extractThemeCmd = &cobra.Command{
	Use:   "theme [name]",
	Short: "Extract changes from a theme inside the container",
	Args:  cobra.ExactArgs(1),
	// Provide dynamic completion for theme names in /home/discourse
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
		// List directories in /home/discourse that are their own git repos
		script := `
set +e
for d in /home/discourse/*/; do
  [ -d "$d" ] || continue
  # Skip hidden directories and known non-theme paths
  b=$(basename "$d")
  case "$b" in
    .*|ai-tools) continue ;;
  esac
  if git -C "$d" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "$b"
  fi
done
`
		out, err := docker.ExecOutput(name, "/home/discourse", []string{"bash", "-lc", script})
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
		themeName := strings.TrimSpace(args[0])
		if themeName == "" {
			return fmt.Errorf("theme name is required")
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

		// Verify theme directory exists
		themePath := filepath.Join("/home/discourse", themeName)
		existsOut, err := docker.ExecOutput(name, "/home/discourse", []string{"bash", "-lc", fmt.Sprintf("[ -d %q ] && echo OK || echo MISSING", themePath)})
		if err != nil || !strings.Contains(existsOut, "OK") {
			return fmt.Errorf("theme '%s' not found at %s", themeName, themePath)
		}

		slug := themeDirSlug(themeName)
		localRepo := filepath.Join(dataDir, fmt.Sprintf("%s_src", slug))
		if customDir != "" {
			localRepo = customDir
		}
		display := fmt.Sprintf("theme %s", themeName)
		return extractWorkspaceRepo(workspaceExtractOptions{
			cmd:              cmd,
			containerName:    name,
			containerWorkdir: themePath,
			localRepo:        localRepo,
			branchName:       themeName,
			displayName:      display,
			chdir:            chdir,
			echoCd:           echoCd,
			syncMode:         syncMode,
			syncDebug:        syncDebug,
		})
	},
}

func init() {
	extractThemeCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
	extractThemeCmd.Flags().String("dir", "", "Extract to a specific directory instead of default location")
	extractThemeCmd.Flags().Bool("chdir", false, "Open a subshell in the extracted repo directory after completion")
	extractThemeCmd.Flags().Bool("echo-cd", false, "Print 'cd <path>' suitable for eval; suppress other output")
	extractThemeCmd.Flags().Bool("sync", false, "Watch for changes and synchronize container <-> host")
	extractThemeCmd.Flags().Bool("debug", false, "Verbose logging for sync mode")
	extractCmd.AddCommand(extractThemeCmd)
}
