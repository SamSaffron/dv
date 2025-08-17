package cli

import (
    "bufio"
    "fmt"
    "io"
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
	Short: "Extract changes from container's code tree into local repo",
	RunE: func(cmd *cobra.Command, args []string) error {
        // Flags controlling post-extract behavior and output
        chdir, _ := cmd.Flags().GetBool("chdir")
        echoCd, _ := cmd.Flags().GetBool("echo-cd")

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
        
        // Determine image associated with this container, falling back to selected image
        imgName := cfg.ContainerImages[name]
        _, imgCfg, err := resolveImage(cfg, imgName)
        if err != nil { return err }
        isTheme := imgCfg.Kind == "theme"
        work := imgCfg.Workdir
        // Check for changes
        status, err := docker.ExecOutput(name, work, []string{"bash", "-lc", "git status --porcelain"})
		if err != nil { return err }
		if strings.TrimSpace(status) == "" { return fmt.Errorf("no changes detected in %s", work) }

        // Configure output behavior. When --echo-cd is requested, suppress normal output so
        // the command can be safely used in command substitution.
        var logOut io.Writer = cmd.OutOrStdout()
        var procOut io.Writer = cmd.OutOrStdout()
        var procErr io.Writer = cmd.ErrOrStderr()
        if echoCd {
            logOut = io.Discard
            // Keep subprocess output and errors on stderr to surface issues without polluting stdout
            procOut = cmd.ErrOrStderr()
            procErr = cmd.ErrOrStderr()
        }

        // Ensure local clone
		var localRepo string
		var repoCloneUrl string
		
		if isTheme {
			// For themes, use the theme name for the local repo
			themeName, err := docker.ExecOutput(name, work, []string{"bash", "-lc", "basename $(pwd)"})
			if err != nil { return err }
			themeName = strings.TrimSpace(themeName)
			
			// Create themes directory if it doesn't exist
			themesDir := filepath.Join(dataDir, "themes")
			if err := os.MkdirAll(themesDir, 0o755); err != nil { return err }
			localRepo = filepath.Join(themesDir, themeName)
			
			// Get the remote URL from the theme repo
			repoCloneUrl, err = docker.ExecOutput(name, work, []string{"bash", "-lc", "git config --get remote.origin.url"})
			repoCloneUrl = strings.TrimSpace(repoCloneUrl)
		} else {
			// For app development, use the discourse_src directory
			localRepo = filepath.Join(dataDir, "discourse_src")
			repoCloneUrl = cfg.DiscourseRepo
		}
        if _, err := os.Stat(localRepo); os.IsNotExist(err) {
            fmt.Fprintf(logOut, "Cloning %s...\n", repoCloneUrl)
            if err := runCmdCapture(procOut, procErr, "git", "clone", repoCloneUrl, localRepo); err != nil { return err }
        } else {
            fmt.Fprintln(logOut, "Using existing repo, resetting...")
            if err := runInDir(localRepo, procOut, procErr, "git", "reset", "--hard", "HEAD"); err != nil { return err }
            if err := runInDir(localRepo, procOut, procErr, "git", "clean", "-fd"); err != nil { return err }
            if err := runInDir(localRepo, procOut, procErr, "git", "fetch", "origin"); err != nil { return err }
        }

		// Get container commit
        commit, err := docker.ExecOutput(name, work, []string{"bash", "-lc", "git rev-parse HEAD"})
		if err != nil { return err }
		commit = strings.TrimSpace(commit)
        fmt.Fprintf(logOut, "Container is at commit: %s\n", commit)

		// Create an appropriate branch prefix based on container type
        branchPrefix := cfg.ExtractBranchPrefix
        if isTheme { branchPrefix = "theme-changes" }
		branch := fmt.Sprintf("%s-%s", branchPrefix, time.Now().Format("20060102-150405"))
        fmt.Fprintf(logOut, "Creating branch: %s\n", branch)
        if err := runInDir(localRepo, procOut, procErr, "git", "checkout", "-b", branch, commit); err != nil { return err }

        fmt.Fprintln(logOut, "Extracting changes from container...")
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
                    fmt.Fprintf(logOut, "Warning: could not copy %s\n", path)
				}
			} else if strings.Contains(status, "D") {
				_ = os.Remove(absDst)
			}
		}
		if err := scanner.Err(); err != nil { return err }

        // If only the cd command is requested, print it cleanly and exit
        if echoCd {
            fmt.Fprintf(cmd.OutOrStdout(), "cd %s\n", localRepo)
            return nil
        }

        fmt.Fprintln(logOut, "")
        fmt.Fprintln(logOut, "✅ Changes extracted successfully!")
        fmt.Fprintf(logOut, "📁 Location: %s\n", localRepo)
        fmt.Fprintf(logOut, "🌿 Branch: %s\n", branch)
        fmt.Fprintf(logOut, "📊 Files changed: %d\n", changedCount)
        fmt.Fprintf(logOut, "🎯 Base commit: %s\n", commit)

        // Optionally drop the user into a subshell rooted at the extracted repo
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
    extractCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
    extractCmd.Flags().Bool("chdir", false, "Open a subshell in the extracted repo directory after completion")
    extractCmd.Flags().Bool("echo-cd", false, "Print 'cd <path>' suitable for eval; suppress other output")
}

func runCmdCapture(stdout, stderr io.Writer, name string, args ...string) error {
    c := exec.Command(name, args...)
    c.Stdout, c.Stderr = stdout, stderr
    return c.Run()
}

func runInDir(dir string, stdout, stderr io.Writer, name string, args ...string) error {
    c := exec.Command(name, args...)
    c.Stdout, c.Stderr = stdout, stderr
    c.Dir = dir
    return c.Run()
}
