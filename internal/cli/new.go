package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

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

		templatePath, _ := cmd.Flags().GetString("template")
		var tpl *templateConfig
		if templatePath != "" {
			var data []byte
			var err error
			if strings.HasPrefix(templatePath, "http://") || strings.HasPrefix(templatePath, "https://") {
				resp, err := http.Get(templatePath)
				if err != nil {
					return fmt.Errorf("fetch template URL: %w", err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					return fmt.Errorf("fetch template URL: %s returned status %d", templatePath, resp.StatusCode)
				}
				data, err = io.ReadAll(resp.Body)
				if err != nil {
					return fmt.Errorf("read template body: %w", err)
				}
			} else {
				data, err = os.ReadFile(templatePath)
				if err != nil {
					return fmt.Errorf("read template: %w", err)
				}
			}
			tpl = &templateConfig{}
			if err := yaml.Unmarshal(data, tpl); err != nil {
				return fmt.Errorf("parse template YAML: %w", err)
			}
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

		if isTruthyEnv("DV_VERBOSE") {
			fmt.Fprintf(cmd.OutOrStdout(), "Resolving image for agent '%s' (image override: '%s')...\n", name, imageOverride)
		}
		// Determine which image to use
		imgName, imgCfg, err := resolveImage(cfg, imageOverride)
		if err != nil {
			return err
		}
		imageTag := imgCfg.Tag
		workdir := imgCfg.Workdir

		sshAuthSock := ""
		if tpl != nil && tpl.Git.SSHForward {
			sshAuthSock = os.Getenv("SSH_AUTH_SOCK")
			if sshAuthSock == "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "Warning: ssh_forward enabled in template but SSH_AUTH_SOCK is not set on host.")
			}
		}

		// Apply template-specific config changes before saving
		if tpl != nil {
			// Add copy rules
			for _, rule := range tpl.Copy {
				rule.Agents = []string{name}
				cfg.CopyRules = append(cfg.CopyRules, rule)
			}
		}

		if isTruthyEnv("DV_VERBOSE") {
			fmt.Fprintf(cmd.OutOrStdout(), "Saving config with selected agent '%s'...\n", name)
		}
		if err := config.Save(configDir, cfg); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Creating agent '%s' from image '%s'...\n", name, imageTag)
		// initialize container by running a no-op command
		if err := ensureContainerRunningWithWorkdir(cmd, cfg, name, workdir, imageTag, imgName, false, sshAuthSock); err != nil {
			return err
		}
		if cfg.ContainerImages == nil {
			cfg.ContainerImages = map[string]string{}
		}
		if isTruthyEnv("DV_VERBOSE") {
			fmt.Fprintf(cmd.OutOrStdout(), "Updating container-image mapping for '%s' to '%s'...\n", name, imgName)
		}
		cfg.ContainerImages[name] = imgName
		_ = config.Save(configDir, cfg)

		// Post-creation template execution
		if tpl != nil {
			if err := executeTemplate(cmd, cfg, name, workdir, tpl, sshAuthSock); err != nil {
				return err
			}
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Agent '%s' is ready and selected.\n", name)
		return nil
	},
}

func checkoutPR(cmd *cobra.Command, cfg config.Config, name, workdir string, prNumber int, envs []string) error {
	owner, repo := prSearchOwnerRepoFromContainer(cfg, name)
	if owner == "" || repo == "" {
		owner, repo = ownerRepoFromURL(cfg.DiscourseRepo)
	}
	if owner == "" || repo == "" {
		return fmt.Errorf("unable to determine repository owner/name")
	}
	prDetail, err := fetchPRDetail(owner, repo, prNumber)
	if err != nil {
		return err
	}
	branchName := prDetail.Head.Ref
	checkoutCmds := buildPRCheckoutCommands(prNumber, branchName)
	script := buildDiscourseResetScript(checkoutCmds)
	return docker.ExecInteractive(name, workdir, envs, []string{"bash", "-lc", script})
}

func checkoutBranch(cmd *cobra.Command, cfg config.Config, name, workdir, branchName string, envs []string) error {
	if branchName == "main" || branchName == "master" {
		fmt.Fprintf(cmd.OutOrStdout(), "Updating %s branch...\n", branchName)
		script := fmt.Sprintf(`
set -e
echo "Checking out %s..."
git checkout %s > /tmp/dv-git-checkout.log 2>&1
echo "Pulling latest..."
git pull > /tmp/dv-git-pull.log 2>&1
`, branchName, branchName)
		return docker.ExecInteractive(name, workdir, envs, []string{"bash", "-lc", script})
	}
	checkoutCmds := buildBranchCheckoutCommands(branchName)
	script := buildDiscourseResetScript(checkoutCmds)
	return docker.ExecInteractive(name, workdir, envs, []string{"bash", "-lc", script})
}

func runMaintenance(cmd *cobra.Command, name, workdir string, envList []string) error {
	fmt.Fprintf(cmd.OutOrStdout(), "Running maintenance (bundle, migrate)...\n")
	script := `
set -e
trap 'echo "Error occurred. Check $FAILED_LOG inside the container for details."; exit 1' ERR

echo "Bundling..."
FAILED_LOG=/tmp/dv-bundle.log
bundle install > $FAILED_LOG 2>&1

echo "Waiting for PostgreSQL to be ready..."
timeout 30 bash -c 'until pg_isready > /dev/null 2>&1; do sleep 1; done' || (echo "PostgreSQL did not become ready"; exit 1)

echo "Migrating dev..."
FAILED_LOG=/tmp/dv-migrate-dev.log
bin/rake db:migrate > $FAILED_LOG 2>&1

echo "Migrating test..."
FAILED_LOG=/tmp/dv-migrate-test.log
RAILS_ENV=test bin/rake db:migrate > $FAILED_LOG 2>&1

echo "Maintenance successful."
`
	return docker.ExecInteractive(name, workdir, envList, []string{"bash", "-lc", script})
}

func executeTemplate(cmd *cobra.Command, cfg config.Config, name, workdir string, tpl *templateConfig, sshAuthSock string) error {
	// 1. Env variables
	envList := collectEnvPassthrough(cfg)
	if len(tpl.Env) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Setting environment variables...\n")
		for k, v := range tpl.Env {
			envList = append(envList, fmt.Sprintf("%s=%s", k, v))
		}
	}

	// 1.5 SSH Forwarding setup
	if tpl.Git.SSHForward && sshAuthSock != "" {
		envList = append(envList, "SSH_AUTH_SOCK=/tmp/ssh-agent.sock")
		fmt.Fprintf(cmd.OutOrStdout(), "Setting up SSH agent forwarding...\n")
		sshSetup := `
mkdir -p ~/.ssh
chmod 700 ~/.ssh
ssh-keyscan github.com >> ~/.ssh/known_hosts 2>/dev/null
`
		if _, err := docker.ExecOutput(name, workdir, []string{"bash", "-lc", sshSetup}); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to setup SSH known_hosts: %v\n", err)
		}
	}

	// 2. Maintenance Mode: Stop Services
	fmt.Fprintf(cmd.OutOrStdout(), "Stopping services for provisioning...\n")
	stopScript := "sudo /usr/bin/sv force-stop unicorn ember-cli || true"
	if _, err := docker.ExecOutput(name, workdir, []string{"bash", "-lc", stopScript}); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to stop services: %v\n", err)
	}

	// Ensure services are restarted even if something fails
	defer func() {
		fmt.Fprintf(cmd.OutOrStdout(), "Starting services (cleanup)...\n")
		startScript := "sudo /usr/bin/sv start unicorn ember-cli || true"
		_, _ = docker.ExecOutput(name, workdir, []string{"bash", "-lc", startScript})
	}()

	// 3. Discourse branch/PR foundation
	if tpl.Discourse.PR != 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Checking out PR %d...\n", tpl.Discourse.PR)
		if err := checkoutPR(cmd, cfg, name, workdir, tpl.Discourse.PR, envList); err != nil {
			return err
		}
	} else if tpl.Discourse.Branch != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Checking out branch %s...\n", tpl.Discourse.Branch)
		if err := checkoutBranch(cmd, cfg, name, workdir, tpl.Discourse.Branch, envList); err != nil {
			return err
		}
	}

	// 4. Repository Operations (Plugins)
	for _, p := range tpl.Plugins {
		pPath := p.Path
		if pPath == "" {
			pPath = path.Join("plugins", path.Base(strings.TrimSuffix(p.Repo, ".git")))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Installing plugin %s into %s...\n", p.Repo, pPath)
		cloneCmd := fmt.Sprintf("git clone %s %s", shellQuote(p.Repo), shellQuote(pPath))
		if p.Branch != "" {
			cloneCmd = fmt.Sprintf("git clone -b %s %s %s", shellQuote(p.Branch), shellQuote(p.Repo), shellQuote(pPath))
		}
		if err := docker.ExecInteractive(name, workdir, envList, []string{"bash", "-lc", cloneCmd}); err != nil {
			return fmt.Errorf("failed to clone plugin %s: %w", p.Repo, err)
		}
	}

	// 5. Maintenance (Bundle and Migrate)
	// Now that core is foundation-ed and plugins are cloned, we bundle and migrate.
	if err := runMaintenance(cmd, name, workdir, envList); err != nil {
		return err
	}

	// 6. On Create Commands (Provisioning)
	for i, c := range tpl.OnCreate {
		fmt.Fprintf(cmd.OutOrStdout(), "Running on_create command: %s...\n", c)
		// Redirecting to a log file inside the container to avoid noise
		logFile := fmt.Sprintf("/tmp/dv-on-create-%d.log", i)
		cmdWithLog := fmt.Sprintf("%s > %s 2>&1", c, logFile)
		if err := docker.ExecInteractive(name, workdir, envList, []string{"bash", "-lc", cmdWithLog}); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "on_create command failed. See log in container at %s\n", logFile)
			return fmt.Errorf("on_create command failed: %s: %w", c, err)
		}
	}

	// 7. Start Services and Wait for Health
	fmt.Fprintf(cmd.OutOrStdout(), "Provisioning complete. Starting Discourse and waiting for it to be ready...\n")
	startScript := "sudo /usr/bin/sv start unicorn ember-cli || true"
	if _, err := docker.ExecOutput(name, workdir, []string{"bash", "-lc", startScript}); err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	// Wait for health check (max 60s)
	healthCmd := "timeout 60 bash -c 'until curl -s -f http://localhost:9292/srv/status > /dev/null 2>&1; do sleep 2; done' || exit 1"
	if _, err := docker.ExecOutput(name, workdir, []string{"bash", "-lc", healthCmd}); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: Discourse did not become healthy within 60s. Some settings might fail.\n")
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Discourse is ready.\n")
	}

	// 8. Post-Boot Configuration (Settings, Themes, MCP)
	// These require the API or a healthy Rails environment

	// Site Settings
	if len(tpl.Settings) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Applying site settings...\n")
		if err := ApplySiteSettings(cmd, cfg, name, tpl.Settings, false, "template"); err != nil {
			return fmt.Errorf("failed to apply site settings: %w", err)
		}
	}

	// Themes
	for _, t := range tpl.Themes {
		fmt.Fprintf(cmd.OutOrStdout(), "Installing theme %s...\n", t.Repo)
		dataDir, _ := xdg.DataDir()
		configDir, _ := xdg.ConfigDir()
		ctx := themeCommandContext{
			cfg:           &cfg,
			configDir:     configDir,
			containerName: name,
			discourseRoot: workdir,
			dataDir:       dataDir,
			verbose:       isTruthyEnv("DV_VERBOSE"),
		}
		if err := handleThemeClone(cmd, ctx, t.Repo, t.Name); err != nil {
			return fmt.Errorf("failed to install theme %s: %w", t.Repo, err)
		}
	}

	// MCP
	for _, m := range tpl.MCP {
		fmt.Fprintf(cmd.OutOrStdout(), "Configuring MCP %s...\n", m.Name)
		mcpCfg := mcpConfiguration{
			name: m.Name,
		}
		if m.Command != "" {
			// Custom MCP
			mcpCfg.registrationCmd = fmt.Sprintf("claude mcp add -s user %s -- %s %s", m.Name, m.Command, strings.Join(m.Args, " "))
			mcpCfg.codexCommand = m.Command
			mcpCfg.codexArgs = m.Args
			mcpCfg.geminiCommand = m.Command
			mcpCfg.geminiArgs = m.Args
			if err := configureMCP(cmd, name, workdir, envList, mcpCfg); err != nil {
				return fmt.Errorf("failed to configure custom MCP %s: %w", m.Name, err)
			}
		} else {
			// Stock MCP (playwright, discourse, chrome-devtools)
			switch m.Name {
			case "playwright":
				if err := configurePlaywrightMCP(cmd, name, workdir, envList); err != nil {
					return err
				}
			case "discourse":
				if err := configureDiscourseMCP(cmd, name, workdir, envList); err != nil {
					return err
				}
			case "chrome-devtools":
				if err := configureChromeDevToolsMCP(cmd, name, workdir, envList); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unknown stock MCP: %s", m.Name)
			}
		}
	}

	return nil
}

func init() {
	newCmd.Flags().String("image", "", "Image to use (defaults to selected image)")
	newCmd.Flags().String("template", "", "Path to a template YAML file")
}
