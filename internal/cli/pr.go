package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

// prCmd implements: dv pr [--name NAME] NUMBER
// - Checks out the given GitHub PR in the container's repo workdir
// - Resets DB and runs migrations and seed (mirrors Dockerfile init)
var prCmd = &cobra.Command{
	Use:   "pr [--name NAME] NUMBER",
	Short: "Checkout a PR in the container and reset DB",
	Args:  cobra.ExactArgs(1),
	// Dynamic completion: list recent PRs with titles and filter by text
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Only complete the first positional arg (PR number)
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		// Load config to determine container and workdir
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		name, _ := cmd.Flags().GetString("name")
		if name == "" {
			name = currentAgentName(cfg)
		}

		// Determine repo owner/name from container remotes (prefer upstream for forks)
		owner, repo := prSearchOwnerRepoFromContainer(cfg, name)
		if owner == "" || repo == "" {
			// Fallback to configured discourse repo
			owner, repo = ownerRepoFromURL(cfg.DiscourseRepo)
		}
		if owner == "" || repo == "" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		// Fetch recent PRs from GitHub API (state=all)
		limit := 100
		if v := os.Getenv("DV_PR_COMPLETE_LIMIT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		q := strings.ToLower(strings.TrimSpace(toComplete))
		// If there is a non-numeric query, prefer GitHub search by title; on failure fallback to recent list
		var prs []ghPR
		var perr error
		usedSearch := false
		if q != "" && !isNumeric(q) {
			prs, perr = searchPRsByTitle(owner, repo, q, limit)
			if perr == nil {
				usedSearch = true
			} else {
				prs, perr = listRecentPRs(owner, repo, limit)
			}
		} else {
			prs, perr = listRecentPRs(owner, repo, limit)
		}
		if perr != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []string
		for _, pr := range prs {
			if !usedSearch && q != "" {
				numStr := strconv.Itoa(pr.Number)
				title := strings.ToLower(pr.Title)
				if isNumeric(q) {
					if !strings.Contains(numStr, q) {
						continue
					}
				} else {
					if !strings.Contains(title, q) {
						continue
					}
				}
			}
			out = append(out, fmt.Sprintf("%d\t%s", pr.Number, pr.Title))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		// Parse PR number
		prNumber, err := strconv.Atoi(strings.TrimSpace(args[0]))
		if err != nil || prNumber <= 0 {
			return fmt.Errorf("invalid PR number: %q", args[0])
		}

		// Load config and container details
		configDir, err := xdg.ConfigDir()
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

		if !docker.Exists(name) {
			fmt.Fprintf(cmd.OutOrStdout(), "Container '%s' does not exist. Run 'dv start' first.\n", name)
			return nil
		}
		if !docker.Running(name) {
			fmt.Fprintf(cmd.OutOrStdout(), "Starting container '%s'...\n", name)
			if err := docker.Start(name); err != nil {
				return err
			}
		}

		// Determine workdir from associated image
		imgName := cfg.ContainerImages[name]
		var imgCfg config.ImageConfig
		if imgName != "" {
			imgCfg = cfg.Images[imgName]
		} else {
			_, imgCfg, err = resolveImage(cfg, "")
			if err != nil {
				return err
			}
		}
		workdir := imgCfg.Workdir
		if strings.TrimSpace(workdir) == "" {
			workdir = "/var/www/discourse"
		}
		if imgCfg.Kind != "discourse" {
			return fmt.Errorf("'dv pr' is only supported for discourse image kind; current: %q", imgCfg.Kind)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Checking out PR #%d in container '%s'...\n", prNumber, name)

		// Build shell script to fetch and checkout PR branch safely
		// Use FETCH_HEAD flow and force local branch update to pr-<num>
		checkoutCmds := buildPRCheckoutCommands(prNumber)
		script := buildDiscourseResetScript(checkoutCmds)

		// Run interactively to stream output to the user
		argv := []string{"bash", "-lc", script}
		if err := docker.ExecInteractive(name, workdir, nil, argv); err != nil {
			return fmt.Errorf("container: failed to checkout PR and migrate: %w", err)
		}
		return nil
	},
}

func init() {
	prCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
	rootCmd.AddCommand(prCmd)
}

// repoOwnerRepoFromContainer tries to read remote.origin.url inside the container
// and parse it for a GitHub owner/repo pair. Returns empty strings on failure.
func repoOwnerRepoFromContainer(cfg config.Config, containerName string) (string, string) {
	// If container isn't running, avoid starting it just for completion
	if !docker.Exists(containerName) || !docker.Running(containerName) {
		return "", ""
	}
	// Determine workdir
	imgName := cfg.ContainerImages[containerName]
	var imgCfg config.ImageConfig
	if imgName != "" {
		imgCfg = cfg.Images[imgName]
	} else {
		_, imgCfg, _ = resolveImage(cfg, "")
	}
	workdir := imgCfg.Workdir
	if strings.TrimSpace(workdir) == "" {
		workdir = "/var/www/discourse"
	}
	// Prefer upstream if present, else fall back to origin
	upOut, _ := docker.ExecOutput(containerName, workdir, []string{"bash", "-lc", "git config --get remote.upstream.url || true"})
	remoteURL := strings.TrimSpace(upOut)
	if remoteURL == "" {
		out, _ := docker.ExecOutput(containerName, workdir, []string{"bash", "-lc", "git config --get remote.origin.url || true"})
		remoteURL = strings.TrimSpace(out)
	}
	if remoteURL == "" {
		return "", ""
	}
	owner, repo := ownerRepoFromURL(remoteURL)
	return owner, repo
}

// prSearchOwnerRepoFromContainer chooses the best owner/repo for PR search.
// Prefer upstream remote; if origin is a fork of 'discourse' repo, normalize owner to 'discourse'.
func prSearchOwnerRepoFromContainer(cfg config.Config, containerName string) (string, string) {
	owner, repo := repoOwnerRepoFromContainer(cfg, containerName)
	if repo == "" {
		return owner, repo
	}
	// Normalize common fork case: searching PRs on upstream 'discourse' rather than fork
	if strings.EqualFold(repo, "discourse") && !strings.EqualFold(owner, "discourse") {
		return "discourse", repo
	}
	return owner, repo
}

// ownerRepoFromURL extracts GitHub owner/repo from common remote URL formats.
// Supports https and ssh formats; strips .git suffix.
func ownerRepoFromURL(url string) (string, string) {
	s := strings.TrimSpace(url)
	s = strings.TrimSuffix(s, ".git")
	// Normalize
	// Examples:
	//  https://github.com/discourse/discourse
	//  git@github.com:discourse/discourse
	//  ssh://git@github.com/discourse/discourse
	var hostIdx int
	if i := strings.Index(s, "github.com"); i >= 0 {
		hostIdx = i + len("github.com")
	} else {
		return "", ""
	}
	tail := s[hostIdx:]
	// Remove leading separators like ':' or '/'
	tail = strings.TrimLeft(tail, ":/")
	parts := strings.Split(tail, "/")
	if len(parts) < 2 {
		return "", ""
	}
	owner := parts[0]
	repo := parts[1]
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if owner == "" || repo == "" {
		return "", ""
	}
	return owner, repo
}

type ghPR struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
	Draft     bool      `json:"draft"`
}

// listRecentPRs queries GitHub REST API for recent PRs (state=all), paginated up to limit.
func listRecentPRs(owner, repo string, limit int) ([]ghPR, error) {
	if limit <= 0 {
		limit = 30
	}
	if limit > 200 {
		limit = 200
	}
	perPage := 100
	if limit < perPage {
		perPage = limit
	}
	var all []ghPR
	page := 1
	client := &http.Client{Timeout: 8 * time.Second}
	for len(all) < limit {
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls?state=all&per_page=%d&page=%d&sort=updated&direction=desc", owner, repo, perPage, page)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "dv-cli")
		if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, fmt.Errorf("GitHub API error: %s", resp.Status)
		}
		var prs []ghPR
		if err := json.NewDecoder(resp.Body).Decode(&prs); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()
		if len(prs) == 0 {
			break
		}
		all = append(all, prs...)
		if len(all) >= limit {
			break
		}
		page++
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// searchPRsByTitle uses GitHub search API to find PRs with title containing query
func searchPRsByTitle(owner, repo, query string, limit int) ([]ghPR, error) {
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	// Use search issues API with in:title filter
	q := fmt.Sprintf("repo:%s/%s+type:pr+in:title+%s", owner, repo, query)
	url := fmt.Sprintf("https://api.github.com/search/issues?q=%s&per_page=%d&sort=updated&order=desc", urlQueryEscape(q), limit)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "dv-cli")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub API error: %s", resp.Status)
	}
	var res struct {
		Items []struct {
			Number    int       `json:"number"`
			Title     string    `json:"title"`
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	out := make([]ghPR, 0, len(res.Items))
	for _, it := range res.Items {
		out = append(out, ghPR{Number: it.Number, Title: it.Title, UpdatedAt: it.UpdatedAt})
	}
	return out, nil
}

// urlQueryEscape performs minimal escaping for GitHub search query
func urlQueryEscape(s string) string {
	// Replace spaces with '+'; leave other characters as-is for simplicity
	r := strings.ReplaceAll(s, " ", "+")
	return r
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
