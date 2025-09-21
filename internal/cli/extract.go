package cli

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
		isTheme := imgCfg.Kind == "theme"
		work := imgCfg.Workdir
		// Check for changes
		status, err := docker.ExecOutput(name, work, []string{"bash", "-lc", "git status --porcelain"})
		if err != nil {
			return err
		}
		if strings.TrimSpace(status) == "" {
			if syncMode {
				status = ""
			} else {
				return fmt.Errorf("no changes detected in %s", work)
			}
		}

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
			if err != nil {
				return err
			}
			themeName = strings.TrimSpace(themeName)

			// Create themes directory if it doesn't exist
			themesDir := filepath.Join(dataDir, "themes")
			if err := os.MkdirAll(themesDir, 0o755); err != nil {
				return err
			}
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
			// Prefer SSH when possible; fall back to HTTPS
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

		// Get container commit and branch
		commit, err := docker.ExecOutput(name, work, []string{"bash", "-lc", "git rev-parse HEAD"})
		if err != nil {
			return err
		}
		commit = strings.TrimSpace(commit)
		containerBranch, err := docker.ExecOutput(name, work, []string{"bash", "-lc", "git rev-parse --abbrev-ref HEAD"})
		if err != nil {
			return err
		}
		containerBranch = strings.TrimSpace(containerBranch)
		fmt.Fprintf(logOut, "Container is at commit: %s\n", commit)
		if containerBranch != "" {
			fmt.Fprintf(logOut, "Container branch: %s\n", containerBranch)
		}

		// Decide local checkout strategy based on availability of commit and container branch state
		branchDisplay := ""
		// Does the commit exist in the local clone (after fetch)?
		commitExists := commitExistsInRepo(localRepo, commit)
		if commitExists {
			if containerBranch != "" && containerBranch != "HEAD" {
				// Ensure the same branch is checked out and points at the container commit
				if err := runInDir(localRepo, procOut, procErr, "git", "checkout", "-B", containerBranch, commit); err != nil {
					return err
				}
				branchDisplay = containerBranch
			} else {
				// Detached HEAD in container; do not create a branch when commit exists
				if err := runInDir(localRepo, procOut, procErr, "git", "checkout", "--detach", commit); err != nil {
					return err
				}
				branchDisplay = "HEAD (detached)"
			}
		} else {
			// Commit is missing in outer repo; create a branch named after the agent
			// Choose a reasonable base: origin/<containerBranch> if it exists, otherwise origin/main or origin/master
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
				} else {
					// Fall back to origin/HEAD if available
					if refExists(localRepo, "origin/HEAD") {
						baseRef = "origin/HEAD"
					}
				}
			}
			// Create or reset the branch named after the agent
			branchName := name
			if baseRef != "" {
				if err := runInDir(localRepo, procOut, procErr, "git", "checkout", "-B", branchName, baseRef); err != nil {
					return err
				}
			} else {
				// As a last resort, create the branch at current HEAD
				if err := runInDir(localRepo, procOut, procErr, "git", "checkout", "-B", branchName); err != nil {
					return err
				}
			}
			branchDisplay = branchName
		}

		fmt.Fprintln(logOut, "Extracting changes from container...")
		scanner := bufio.NewScanner(strings.NewReader(status))
		changedCount := 0
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
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
		if err := scanner.Err(); err != nil {
			return err
		}

		// If only the cd command is requested, print it cleanly and exit
		if echoCd {
			fmt.Fprintf(cmd.OutOrStdout(), "cd %s\n", localRepo)
			return nil
		}

		fmt.Fprintln(logOut, "")
		fmt.Fprintln(logOut, "✅ Changes extracted successfully!")
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
				containerWorkdir: work,
				localRepo:        localRepo,
				logOut:           logOut,
				errOut:           cmd.ErrOrStderr(),
				debug:            syncDebug,
			})
		}

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
	extractCmd.Flags().Bool("sync", false, "Watch for changes and synchronize container ↔ host")
	extractCmd.Flags().Bool("debug", false, "Verbose logging for sync mode")
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

// commitExistsInRepo returns true if the given commit SHA exists in the repo.
func commitExistsInRepo(repoDir string, commit string) bool {
	if strings.TrimSpace(commit) == "" {
		return false
	}
	c := exec.Command("git", "cat-file", "-e", commit+"^{commit}")
	c.Dir = repoDir
	if err := c.Run(); err != nil {
		return false
	}
	return true
}

// refExists returns true if the given ref (e.g., origin/main) resolves in the repo.
func refExists(repoDir string, ref string) bool {
	if strings.TrimSpace(ref) == "" {
		return false
	}
	c := exec.Command("git", "rev-parse", "--verify", "--quiet", ref)
	c.Dir = repoDir
	if err := c.Run(); err != nil {
		return false
	}
	return true
}

// makeCloneCandidates returns preferred clone URLs: SSH first if derivable, then original, then HTTPS fallbacks.
func makeCloneCandidates(original string) []string {
	var candidates []string
	// Try to derive SSH from the original
	if ssh, ok := toSSH(original); ok {
		candidates = append(candidates, ssh)
	}
	// Always include the original as next try to respect explicit config
	candidates = append(candidates, original)
	// And finally try a HTTPS form if derivable and different from original
	if https, ok := toHTTPS(original); ok && https != original {
		candidates = append(candidates, https)
	}
	// Deduplicate while preserving order
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if _, exists := seen[c]; exists {
			continue
		}
		seen[c] = struct{}{}
		unique = append(unique, c)
	}
	return unique
}

// toSSH converts common HTTPS/SSH URL forms into scp-like SSH (git@host:path) when possible.
func toSSH(raw string) (string, bool) {
	// Already in git@host:path form
	if strings.HasPrefix(raw, "git@") && strings.Contains(raw, ":") {
		return raw, true
	}
	// ssh://git@host/owner/repo.git
	if strings.HasPrefix(strings.ToLower(raw), "ssh://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "", false
		}
		user := u.User.Username()
		if user == "" {
			user = "git"
		}
		p := strings.TrimPrefix(u.Path, "/")
		if p == "" {
			return "", false
		}
		return fmt.Sprintf("%s@%s:%s", user, u.Host, p), true
	}
	// https://host/owner/repo(.git)
	if strings.HasPrefix(strings.ToLower(raw), "http://") || strings.HasPrefix(strings.ToLower(raw), "https://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "", false
		}
		user := "git"
		p := strings.TrimPrefix(u.Path, "/")
		if p == "" {
			return "", false
		}
		return fmt.Sprintf("%s@%s:%s", user, u.Host, p), true
	}
	return "", false
}

// toHTTPS converts git@host:path and ssh:// URLs to https://host/path form when possible.
func toHTTPS(raw string) (string, bool) {
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return raw, true
	}
	if strings.HasPrefix(raw, "git@") && strings.Contains(raw, ":") {
		// git@host:owner/repo(.git)
		parts := strings.SplitN(strings.TrimPrefix(raw, "git@"), ":", 2)
		if len(parts) != 2 {
			return "", false
		}
		host := parts[0]
		path := parts[1]
		if strings.TrimSpace(host) == "" || strings.TrimSpace(path) == "" {
			return "", false
		}
		return fmt.Sprintf("https://%s/%s", host, path), true
	}
	if strings.HasPrefix(lower, "ssh://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "", false
		}
		p := strings.TrimPrefix(u.Path, "/")
		if p == "" {
			return "", false
		}
		return fmt.Sprintf("https://%s/%s", u.Host, p), true
	}
	return "", false
}

// cloneWithFallback attempts to clone using each URL until one succeeds.
func cloneWithFallback(stdout, stderr io.Writer, urls []string, dest string) error {
	var errs []string
	for _, u := range urls {
		fmt.Fprintf(stderr, "git clone %s %s\n", u, dest)
		if err := runCmdCapture(stdout, stderr, "git", "clone", u, dest); err == nil {
			return nil
		} else {
			errs = append(errs, fmt.Sprintf("%s: %v", u, err))
		}
	}
	return fmt.Errorf("all clone attempts failed:\n%s", strings.Join(errs, "\n"))
}
