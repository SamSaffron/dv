package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
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
		out, err := docker.ExecOutput(name, work, []string{"bash", "-lc", script})
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
		existsOut, err := docker.ExecOutput(name, work, []string{"bash", "-lc", fmt.Sprintf("[ -d %q ] && echo OK || echo MISSING", pluginRel)})
		if err != nil || !strings.Contains(existsOut, "OK") {
			return fmt.Errorf("plugin '%s' not found in %s", pluginName, filepath.Join(work, "plugins"))
		}

		pluginWork := filepath.Join(work, "plugins", pluginName)

		// Detect if plugin is a git repo separate from core
		isRepoOut, _ := docker.ExecOutput(name, pluginWork, []string{"bash", "-lc", "git rev-parse --is-inside-work-tree 2>/dev/null || echo false"})
		isRepo := strings.Contains(strings.ToLower(isRepoOut), "true")

		// Configure output behavior
		var logOut io.Writer = cmd.OutOrStdout()
		var procOut io.Writer = cmd.OutOrStdout()
		var procErr io.Writer = cmd.ErrOrStderr()
		if echoCd {
			logOut = io.Discard
			procOut = cmd.ErrOrStderr()
			procErr = cmd.ErrOrStderr()
		}

		// Prepare destination
		localRepo := filepath.Join(dataDir, fmt.Sprintf("%s_src", pluginName))

		if !isRepo {
			// Non-git plugin: copy entire directory into destination
			_ = os.RemoveAll(localRepo)
			if err := os.MkdirAll(localRepo, 0o755); err != nil {
				return err
			}
			fmt.Fprintln(logOut, "Copying plugin directory (non-git repo detected)...")
			// Copy contents rather than nesting
			if err := docker.CopyFromContainer(name, filepath.Join(pluginWork, "."), localRepo); err != nil {
				return err
			}
			if echoCd {
				fmt.Fprintf(cmd.OutOrStdout(), "cd %s\n", localRepo)
				return nil
			}
			fmt.Fprintln(logOut, "")
			fmt.Fprintln(logOut, "✅ Plugin copied successfully!")
			fmt.Fprintf(logOut, "📁 Location: %s\n", localRepo)
			if chdir {
				shell := os.Getenv("SHELL")
				if strings.TrimSpace(shell) == "" {
					shell = "/bin/bash"
				}
				s := exec.Command(shell)
				s.Dir = localRepo
				s.Stdin = os.Stdin
				s.Stdout = os.Stdout
				s.Stderr = os.Stderr
				return s.Run()
			}
			return nil
		}

		// Git-backed plugin: proceed similar to core extract
		status, err := docker.ExecOutput(name, pluginWork, []string{"bash", "-lc", "git status --porcelain"})
		if err != nil {
			return err
		}
		if strings.TrimSpace(status) == "" {
			if syncMode {
				status = ""
			} else {
				return fmt.Errorf("no changes detected in %s", pluginWork)
			}
		}

		// Determine repo URL
		repoCloneUrl, _ := docker.ExecOutput(name, pluginWork, []string{"bash", "-lc", "git config --get remote.origin.url"})
		repoCloneUrl = strings.TrimSpace(repoCloneUrl)
		if repoCloneUrl == "" {
			// Fallback: if no remote, treat like non-git copy
			_ = os.RemoveAll(localRepo)
			if err := os.MkdirAll(localRepo, 0o755); err != nil {
				return err
			}
			fmt.Fprintln(logOut, "Copying plugin directory (no remote detected)...")
			if err := docker.CopyFromContainer(name, filepath.Join(pluginWork, "."), localRepo); err != nil {
				return err
			}
			if echoCd {
				fmt.Fprintf(cmd.OutOrStdout(), "cd %s\n", localRepo)
				return nil
			}
			fmt.Fprintln(logOut, "")
			fmt.Fprintln(logOut, "✅ Plugin copied successfully!")
			fmt.Fprintf(logOut, "📁 Location: %s\n", localRepo)
			if chdir {
				shell := os.Getenv("SHELL")
				if strings.TrimSpace(shell) == "" {
					shell = "/bin/bash"
				}
				s := exec.Command(shell)
				s.Dir = localRepo
				s.Stdin = os.Stdin
				s.Stdout = os.Stdout
				s.Stderr = os.Stderr
				return s.Run()
			}
			return nil
		}

		// Ensure local clone
		if _, err := os.Stat(localRepo); os.IsNotExist(err) {
			candidates := makeCloneCandidates(repoCloneUrl)
			fmt.Fprintf(logOut, "Cloning (trying %d URL(s))...\n", len(candidates))
			if err := cloneWithFallback(procOut, procErr, candidates, localRepo); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(logOut, "Using existing repo, resetting...")
			if err := runInDir(localRepo, procOut, procErr, "git", "reset", "--hard", "HEAD"); err != nil {
				return err
			}
			if err := runInDir(localRepo, procOut, procErr, "git", "clean", "-fd"); err != nil {
				return err
			}
			if err := runInDir(localRepo, procOut, procErr, "git", "fetch", "origin"); err != nil {
				return err
			}
		}

		// Get container commit and branch for plugin
		commit, err := docker.ExecOutput(name, pluginWork, []string{"bash", "-lc", "git rev-parse HEAD"})
		if err != nil {
			return err
		}
		commit = strings.TrimSpace(commit)
		containerBranch, err := docker.ExecOutput(name, pluginWork, []string{"bash", "-lc", "git rev-parse --abbrev-ref HEAD"})
		if err != nil {
			return err
		}
		containerBranch = strings.TrimSpace(containerBranch)
		fmt.Fprintf(logOut, "Container plugin is at commit: %s\n", commit)
		if containerBranch != "" {
			fmt.Fprintf(logOut, "Container plugin branch: %s\n", containerBranch)
		}

		// Checkout strategy
		branchDisplay := ""
		commitExists := commitExistsInRepo(localRepo, commit)
		if commitExists {
			if containerBranch != "" && containerBranch != "HEAD" {
				if err := runInDir(localRepo, procOut, procErr, "git", "checkout", "-B", containerBranch, commit); err != nil {
					return err
				}
				branchDisplay = containerBranch
			} else {
				if err := runInDir(localRepo, procOut, procErr, "git", "checkout", "--detach", commit); err != nil {
					return err
				}
				branchDisplay = "HEAD (detached)"
			}
		} else {
			baseRef := ""
			if containerBranch != "" && containerBranch != "HEAD" {
				candidate := "origin/" + containerBranch
				if refExists(localRepo, candidate) {
					baseRef = candidate
				}
			}
			if baseRef == "" {
				if refExists(localRepo, "origin/main") {
					baseRef = "origin/main"
				} else if refExists(localRepo, "origin/master") {
					baseRef = "origin/master"
				} else if refExists(localRepo, "origin/HEAD") {
					baseRef = "origin/HEAD"
				}
			}
			branchName := pluginName
			if baseRef != "" {
				if err := runInDir(localRepo, procOut, procErr, "git", "checkout", "-B", branchName, baseRef); err != nil {
					return err
				}
			} else {
				if err := runInDir(localRepo, procOut, procErr, "git", "checkout", "-B", branchName); err != nil {
					return err
				}
			}
			branchDisplay = branchName
		}

		// Extract changes
		fmt.Fprintln(logOut, "Extracting plugin changes from container...")
		scanner := bufio.NewScanner(strings.NewReader(status))
		changedCount := 0
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			changedCount++
			st := line[:2]
			rel := strings.TrimSpace(line[3:])
			absDst := filepath.Join(localRepo, rel)
			if st == "??" || strings.ContainsAny(st, "AM") {
				_ = os.MkdirAll(filepath.Dir(absDst), 0o755)
				if err := docker.CopyFromContainer(name, filepath.Join(pluginWork, rel), absDst); err != nil {
					fmt.Fprintf(logOut, "Warning: could not copy %s\n", rel)
				}
			} else if strings.Contains(st, "D") {
				_ = os.Remove(absDst)
			}
		}
		if err := scanner.Err(); err != nil {
			return err
		}

		if echoCd {
			fmt.Fprintf(cmd.OutOrStdout(), "cd %s\n", localRepo)
			return nil
		}

		fmt.Fprintln(logOut, "")
		fmt.Fprintln(logOut, "✅ Plugin changes extracted successfully!")
		fmt.Fprintf(logOut, "📁 Location: %s\n", localRepo)
		if strings.TrimSpace(branchDisplay) != "" {
			fmt.Fprintf(logOut, "🌿 Branch: %s\n", branchDisplay)
		}
		fmt.Fprintf(logOut, "📊 Files changed: %d\n", changedCount)
		fmt.Fprintf(logOut, "🎯 Base commit: %s\n", commit)

		if syncMode {
			if changedCount == 0 {
				fmt.Fprintln(logOut, "No pending changes detected; watching for new modifications...")
			}
			fmt.Fprintln(logOut, "🔄 Entering sync mode; press Ctrl+C to stop.")
			return runExtractSync(cmd, syncOptions{
				containerName:    name,
				containerWorkdir: pluginWork,
				localRepo:        localRepo,
				logOut:           logOut,
				errOut:           cmd.ErrOrStderr(),
				debug:            syncDebug,
			})
		}

		if chdir {
			shell := os.Getenv("SHELL")
			if strings.TrimSpace(shell) == "" {
				shell = "/bin/bash"
			}
			s := exec.Command(shell)
			s.Dir = localRepo
			s.Stdin = os.Stdin
			s.Stdout = os.Stdout
			s.Stderr = os.Stderr
			return s.Run()
		}

		return nil
	},
}

func init() {
	extractPluginCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
	extractPluginCmd.Flags().Bool("chdir", false, "Open a subshell in the extracted repo directory after completion")
	extractPluginCmd.Flags().Bool("echo-cd", false, "Print 'cd <path>' suitable for eval; suppress other output")
	extractPluginCmd.Flags().Bool("sync", false, "Watch for changes and synchronize container ↔ host")
	extractPluginCmd.Flags().Bool("debug", false, "Verbose logging for sync mode")
	extractCmd.AddCommand(extractPluginCmd)
}
