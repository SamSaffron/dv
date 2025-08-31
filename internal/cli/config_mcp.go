package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var configMcpCmd = &cobra.Command{
	Use:   "mcp NAME",
	Short: "Configure an MCP server in the running container",
	Args:  cobra.ExactArgs(1),
	ValidArgs: []string{
		"playwright",
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		containerName, _ := cmd.Flags().GetString("name")
		if containerName == "" {
			containerName = currentAgentName(cfg)
		}

		if !docker.Exists(containerName) {
			fmt.Fprintf(cmd.OutOrStdout(), "Container '%s' does not exist. Run 'dv start' first.\n", containerName)
			return nil
		}
		if !docker.Running(containerName) {
			fmt.Fprintf(cmd.OutOrStdout(), "Starting container '%s'...\n", containerName)
			if err := docker.Start(containerName); err != nil {
				return err
			}
		}

		// Determine workdir from the associated image if known; fall back to selected image
		imgName := cfg.ContainerImages[containerName]
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

		mcpName := strings.ToLower(strings.TrimSpace(args[0]))
		if mcpName != "playwright" {
			return fmt.Errorf("unsupported MCP name: %s (supported: playwright)", mcpName)
		}

		// Prepare env pass-through so tools like 'claude' have credentials
		envs := make([]string, 0, len(cfg.EnvPassthrough))
		for _, key := range cfg.EnvPassthrough {
			if val, ok := os.LookupEnv(key); ok && val != "" {
				envs = append(envs, key)
			}
		}
		if _, ok := os.LookupEnv("ANTHROPIC_API_KEY"); !ok {
			fmt.Fprintln(cmd.ErrOrStderr(), "Warning: ANTHROPIC_API_KEY is not set on host; 'claude' may fail.")
		}

		// For now we only support Claude registration of Playwright MCP
		removeEchoCmd := "claude mcp remove -s user playwright"
		removeCmd := removeEchoCmd + " || true"
		registrationCmd := "claude mcp add -s user playwright -- npx -y @playwright/mcp@latest --isolated --no-sandbox --headless --executable-path /usr/bin/chromium"

		// Attempt removal first to allow updates
		fmt.Fprintf(cmd.OutOrStdout(), "Ensuring no existing Claude MCP '%s' remains (safe to ignore failures)...\n", mcpName)
		fmt.Fprintf(cmd.OutOrStdout(), "Running: %s\n\n", removeEchoCmd)
		if err := docker.ExecInteractive(containerName, workdir, envs, []string{"bash", "-lc", removeCmd}); err != nil {
			// Ignore errors from removal; proceed to add
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Registering MCP '%s' with Claude inside container '%s' as 'discourse'...\n", mcpName, containerName)
		fmt.Fprintf(cmd.OutOrStdout(), "Running: %s\n\n", registrationCmd)

		// Run interactively to stream output to the user
		argv := []string{"bash", "-lc", registrationCmd}
		if err := docker.ExecInteractive(containerName, workdir, envs, argv); err != nil {
			return err
		}

		// Also configure Codex CLI TOML at ~/.codex/config.toml
		fmt.Fprintln(cmd.OutOrStdout(), "\nConfiguring Codex to use the Playwright MCP (updating ~/.codex/config.toml)...")

		// Ensure directory exists inside container
		_, _ = docker.ExecOutput(containerName, workdir, []string{"bash", "-lc", "mkdir -p ~/.codex"})

		// Detect existing config
		existsOut, _ := docker.ExecOutput(containerName, workdir, []string{"bash", "-lc", "test -f ~/.codex/config.toml && echo EXISTS || echo MISSING"})
		hasConfig := strings.Contains(existsOut, "EXISTS")

		// Prepare temporary file on host
		tmpDir, err := os.MkdirTemp("", "codex-config-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		hostCfg := filepath.Join(tmpDir, "config.toml")

		var content string
		if hasConfig {
			// Copy existing file out
			if err := docker.CopyFromContainer(containerName, "/home/discourse/.codex/config.toml", hostCfg); err != nil {
				return fmt.Errorf("failed to copy existing Codex config: %w", err)
			}
			b, err := os.ReadFile(hostCfg)
			if err != nil {
				return err
			}
			content = string(b)
		} else {
			content = ""
		}

		// Construct Playwright MCP section
		sectionHeader := "mcp_servers.playwright"
		sectionBody := strings.Join([]string{
			"[" + sectionHeader + "]",
			"command = \"npx\"",
			"args = [\"-y\", \"@playwright/mcp@latest\", \"--isolated\", \"--no-sandbox\", \"--headless\", \"--executable-path\", \"/usr/bin/chromium\"]",
			"",
		}, "\n")

		updated := addOrReplaceTomlSection(content, sectionHeader, sectionBody)
		if err := os.WriteFile(hostCfg, []byte(updated), 0o644); err != nil {
			return err
		}

		// Copy updated config back into container and fix ownership
		if err := docker.CopyToContainer(containerName, hostCfg, "/home/discourse/.codex/config.toml"); err != nil {
			return fmt.Errorf("failed to copy Codex config into container: %w", err)
		}
		_, _ = docker.ExecAsRoot(containerName, workdir, []string{"chown", "discourse:discourse", "/home/discourse/.codex/config.toml"})

		fmt.Fprintln(cmd.OutOrStdout(), "Done.")
		return nil
	},
}

func init() {
	configMcpCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
	configCmd.AddCommand(configMcpCmd)
}

// addOrReplaceTomlSection inserts or replaces a TOML table section defined by sectionHeader
// (e.g., "mcp_servers.playwright"). The sectionBody should include the full header line and
// any key/value lines below it, and may end with a trailing newline.
func addOrReplaceTomlSection(existing string, sectionHeader string, sectionBody string) string {
	// Normalize endings
	existing = strings.ReplaceAll(existing, "\r\n", "\n")
	lines := []string{}
	if strings.TrimSpace(existing) != "" {
		lines = strings.Split(existing, "\n")
	}
	headerLine := "[" + sectionHeader + "]"

	// Find existing section
	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == headerLine {
			start = i
			break
		}
	}

	if start == -1 {
		// Append section to the end
		var b strings.Builder
		if strings.TrimSpace(existing) != "" {
			b.WriteString(strings.TrimRight(existing, "\n"))
			b.WriteString("\n\n")
		}
		b.WriteString(strings.TrimRight(sectionBody, "\n"))
		b.WriteString("\n")
		return b.String()
	}

	// Determine end of existing section (next header or EOF)
	end := len(lines)
	for j := start + 1; j < len(lines); j++ {
		if strings.HasPrefix(strings.TrimSpace(lines[j]), "[") {
			end = j
			break
		}
	}
	// Rebuild with replacement
	var out []string
	out = append(out, lines[:start]...)
	// Add new body (without trailing newline to manage joins consistently)
	for _, l := range strings.Split(strings.TrimRight(sectionBody, "\n"), "\n") {
		out = append(out, l)
	}
	if end < len(lines) {
		// Ensure a blank line between sections if not already present
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, lines[end:]...)
	}
	// Ensure trailing newline
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}
