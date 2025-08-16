package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var extractCmd = &cobra.Command{
	Use:   "extract",
	Short: "Extract changes from container's Discourse tree into local repo",
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil { return err }
		dataDir, err := xdg.DataDir()
		if err != nil { return err }
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil { return err }

		name, _ := cmd.Flags().GetString("name")
		if name == "" { name = currentAgentName(cfg) }

		if !docker.Running(name) {
			return fmt.Errorf("container '%s' is not running; run 'dv run' first", name)
		}

		work := cfg.Workdir
		// Check for changes
		status, err := docker.ExecOutput(name, work, []string{"bash", "-lc", "git status --porcelain"})
		if err != nil { return err }
		if strings.TrimSpace(status) == "" { return fmt.Errorf("no changes detected in %s", work) }

		// Ensure local clone
		localRepo := filepath.Join(dataDir, "discourse_src")
		if _, err := os.Stat(localRepo); os.IsNotExist(err) {
			fmt.Fprintln(cmd.OutOrStdout(), "Cloning discourse/discourse...")
			if err := runCmdCapture("git", "clone", cfg.DiscourseRepo, localRepo); err != nil { return err }
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Using existing discourse repo, resetting...")
			if err := runInDir(localRepo, "git", "reset", "--hard", "HEAD"); err != nil { return err }
			if err := runInDir(localRepo, "git", "clean", "-fd"); err != nil { return err }
			if err := runInDir(localRepo, "git", "fetch", "origin"); err != nil { return err }
		}

		// Get container commit
		commit, err := docker.ExecOutput(name, work, []string{"bash", "-lc", "git rev-parse HEAD"})
		if err != nil { return err }
		commit = strings.TrimSpace(commit)
		fmt.Fprintf(cmd.OutOrStdout(), "Container is at commit: %s\n", commit)

		branch := fmt.Sprintf("%s-%s", cfg.ExtractBranchPrefix, time.Now().Format("20060102-150405"))
		fmt.Fprintf(cmd.OutOrStdout(), "Creating branch: %s\n", branch)
		if err := runInDir(localRepo, "git", "checkout", "-b", branch, commit); err != nil { return err }

		fmt.Fprintln(cmd.OutOrStdout(), "Extracting changes from container...")
		scanner := bufio.NewScanner(strings.NewReader(status))
		changedCount := 0
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" { continue }
			changedCount++
			status := line[:2]
			path := strings.TrimSpace(line[3:])
			absDst := filepath.Join(localRepo, path)
			if status == "??" || strings.ContainsAny(status, "AM") {
				_ = os.MkdirAll(filepath.Dir(absDst), 0o755)
				if err := docker.CopyFromContainer(name, filepath.Join(work, path), absDst); err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Warning: could not copy %s\n", path)
				}
			} else if strings.Contains(status, "D") {
				_ = os.Remove(absDst)
			}
		}
		if err := scanner.Err(); err != nil { return err }

		fmt.Fprintln(cmd.OutOrStdout(), "")
		fmt.Fprintln(cmd.OutOrStdout(), "✅ Changes extracted successfully!")
		fmt.Fprintf(cmd.OutOrStdout(), "📁 Location: %s\n", localRepo)
		fmt.Fprintf(cmd.OutOrStdout(), "🌿 Branch: %s\n", branch)
		fmt.Fprintf(cmd.OutOrStdout(), "📊 Files changed: %d\n", changedCount)
		fmt.Fprintf(cmd.OutOrStdout(), "🎯 Base commit: %s\n", commit)

		return nil
	},
}

func init() {
	extractCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
}

func runCmdCapture(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

func runInDir(dir, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	c.Dir = dir
	return c.Run()
}
