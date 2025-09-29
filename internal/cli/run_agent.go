package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	textarea "github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

// runAgentCmd implements `dv run-agent` (alias: `ra`).
// Usage:
//
//	dv ra <agent> [prompt_file|prompt words...]
//	dv ra <agent> -- [raw agent args...]
//
// If no prompt/args provided, an editor is opened to enter a multiline prompt.
// Prompt files are read from ~/.config/dv/prompts/ and autocompleted.
var runAgentCmd = &cobra.Command{
	Use:     "run-agent [--name NAME] AGENT [PROMPT_FILE|-- ARGS...|PROMPT ...]",
	Aliases: []string{"ra"},
	Short:   "Run an AI agent inside the container with a prompt or prompt file",
	Args:    cobra.MinimumNArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// First arg: agent name completion
		if len(args) == 0 {
			// Build from known rules
			var suggestions []string
			for name := range agentRules {
				suggestions = append(suggestions, name)
			}
			// Filter by prefix
			var out []string
			pref := strings.ToLower(strings.TrimSpace(toComplete))
			for _, s := range suggestions {
				if pref == "" || strings.HasPrefix(strings.ToLower(s), pref) {
					out = append(out, s)
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		}

		// Second arg: prompt file completion
		if len(args) == 1 {
			// If the user appears to be typing a filesystem path, defer to the shell's default
			// file completion so regular files can be selected as prompts.
			if strings.HasPrefix(toComplete, "./") || strings.HasPrefix(toComplete, "../") || strings.HasPrefix(toComplete, "/") || strings.Contains(toComplete, string(os.PathSeparator)) {
				return nil, cobra.ShellCompDirectiveDefault
			}

			configDir, err := xdg.ConfigDir()
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}

			promptsDir := filepath.Join(configDir, "prompts")
			entries, err := os.ReadDir(promptsDir)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}

			var suggestions []string
			for _, entry := range entries {
				if !entry.IsDir() {
					suggestions = append(suggestions, entry.Name())
				}
			}

			// Filter by prefix
			var out []string
			pref := strings.ToLower(strings.TrimSpace(toComplete))
			for _, s := range suggestions {
				if pref == "" || strings.HasPrefix(strings.ToLower(s), pref) {
					out = append(out, s)
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		}

		// No completion for additional args
		return nil, cobra.ShellCompDirectiveNoFileComp
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

		name, _ := cmd.Flags().GetString("name")
		if name == "" {
			name = currentAgentName(cfg)
		}

		// Ensure container exists and is running (match behavior of `enter`)
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

		// Resolve workdir from image associated with container
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

		// Copy configured files (auth, etc.) into the container as in `enter`
		for hostSrc, containerDst := range cfg.CopyFiles {
			hostPath := expandHostPath(hostSrc)
			if hostPath == "" {
				continue
			}
			if st, err := os.Stat(hostPath); err != nil || !st.Mode().IsRegular() {
				fmt.Fprintf(cmd.ErrOrStderr(), "Skipping copy (not found): %s -> %s\n", hostPath, containerDst)
				continue
			}
			// Ensure destination directory exists inside container (as discourse user)
			dstDir := filepath.Dir(containerDst)
			_, _ = docker.ExecOutput(name, workdir, []string{"bash", "-lc", "mkdir -p " + shellQuote(dstDir)})
			if err := docker.CopyToContainer(name, hostPath, containerDst); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Failed to copy %s to %s: %v\n", hostPath, containerDst, err)
				continue
			}
			_, _ = docker.ExecAsRoot(name, workdir, []string{"chown", "discourse:discourse", containerDst})
		}

		// Prepare env pass-through like `enter`: only configured keys
		envs := make([]string, 0, len(cfg.EnvPassthrough)+4)
		for _, key := range cfg.EnvPassthrough {
			if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
				// docker exec -e KEY will copy host value
				envs = append(envs, key)
			}
		}
		// Ensure a sane runtime environment for discourse user
		envs = append(envs,
			"HOME=/home/discourse",
			"USER=discourse",
			"SHELL=/bin/bash",
		)

		// Parse args: first token is the agent name
		agent := args[0]
		rest := args[1:]

		// If user provided "--" treat everything after as raw agent args (no prompt wrapping)
		rawArgs := []string{}
		if len(rest) > 0 {
			for i, a := range rest {
				if a == "--" {
					rawArgs = append(rawArgs, rest[i+1:]...)
					rest = rest[:i]
					break
				}
			}
		}

		// Check if the first argument after agent is a prompt file
		var promptFromFile string
		if len(rest) > 0 {
			firstArg := rest[0]
			// 1) Prefer an actual host filesystem path if it exists (supports relative/absolute)
			hostPath := expandHostPath(firstArg)
			if st, err := os.Stat(hostPath); err == nil && st.Mode().IsRegular() {
				if content, err2 := os.ReadFile(hostPath); err2 == nil {
					promptFromFile = strings.TrimSpace(string(content))
					rest = rest[1:]
				}
			} else {
				// 2) Fallback to a named prompt under ~/.config/dv/prompts
				promptsDir := filepath.Join(configDir, "prompts")
				promptFilePath := filepath.Join(promptsDir, firstArg)
				if st2, err3 := os.Stat(promptFilePath); err3 == nil && st2.Mode().IsRegular() {
					if content, err4 := os.ReadFile(promptFilePath); err4 == nil {
						promptFromFile = strings.TrimSpace(string(content))
						rest = rest[1:]
					}
				}
			}
		}

		// Build the argv to run inside the container using internal rules.
		var argv []string
		switch {
		case len(rawArgs) > 0:
			argv = append([]string{agent}, rawArgs...)
			// If this is a pure help request, capture output via non-TTY exec
			if isHelpArgs(rawArgs) {
				shellCmd := withUserPaths(shellJoin(argv))
				out, err := docker.ExecOutput(name, workdir, []string{"bash", "-lc", shellCmd})
				if err != nil {
					fmt.Fprint(cmd.ErrOrStderr(), out)
					return err
				}
				fmt.Fprint(cmd.OutOrStdout(), out)
				return nil
			}
		case promptFromFile != "":
			// Prompt from file -> construct one-shot invocation with implicit bypass flags
			argv = buildAgentArgs(agent, promptFromFile)
		case len(rest) == 0:
			// No prompt provided -> run interactively with implicit bypass flags
			argv = buildAgentInteractive(agent)
		default:
			// Prompt provided -> construct one-shot invocation with implicit bypass flags
			prompt := strings.Join(rest, " ")
			argv = buildAgentArgs(agent, prompt)
		}

		// Execute inside container through a login shell to pick up PATH/rc files
		shellCmd := withUserPaths(shellJoin(argv))
		return docker.ExecInteractive(name, workdir, envs, []string{"bash", "-lc", shellCmd})
	},
}

// collectPromptInteractive opens $EDITOR for a multiline prompt; falls back to terminal input if needed.
func collectPromptInteractive(cmd *cobra.Command) (string, error) {
	// Use a small Bubble Tea textarea for multiline prompt collection
	m := newPromptModel()
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return "", err
	}
	pm, ok := final.(promptModel)
	if !ok {
		return "", fmt.Errorf("unexpected model type")
	}
	if pm.canceled {
		return "", nil
	}
	return strings.TrimSpace(pm.ta.Value()), nil
}

// buildAgentArgs uses internal, hard-coded rules per agent to construct argv.
// If the agent is unknown, falls back to positional prompt.
func buildAgentArgs(agent string, prompt string) []string {
	if rule, ok := agentRules[strings.ToLower(agent)]; ok {
		base := rule.withPrompt(prompt)
		if len(rule.defaults) > 0 {
			base = injectDefaults(base, rule.defaults)
		}
		return base
	}
	return []string{agent, prompt}
}

func buildAgentInteractive(agent string) []string {
	if rule, ok := agentRules[strings.ToLower(agent)]; ok {
		base := rule.interactive()
		if len(rule.defaults) > 0 {
			base = injectDefaults(base, rule.defaults)
		}
		if len(rule.interactiveDefaults) > 0 {
			base = injectDefaults(base, rule.interactiveDefaults)
		}
		return base
	}
	return []string{agent}
}

func injectDefaults(argv []string, defaults []string) []string {
	if len(argv) == 0 || len(defaults) == 0 {
		return argv
	}
	out := make([]string, 0, len(argv)+len(defaults))
	out = append(out, argv[0])
	// If the command has a subcommand as argv[1] (heuristic: not a flag), keep it before flags
	startIdx := 1
	if len(argv) > 1 && !strings.HasPrefix(argv[1], "-") {
		out = append(out, argv[1])
		startIdx = 2
	}
	out = append(out, defaults...)
	out = append(out, argv[startIdx:]...)
	return out
}

// agentRules defines how to run each supported agent.
type agentRule struct {
	interactive         func() []string
	withPrompt          func(prompt string) []string
	defaults            []string
	interactiveDefaults []string
}

var agentRules = map[string]agentRule{
	"cursor": {
		interactive: func() []string { return []string{"cursor-agent"} },
		withPrompt:  func(p string) []string { return []string{"cursor-agent", "-p", p} },
		defaults:    []string{"-f"},
	},
	"codex": {
		interactive: func() []string { return []string{"codex"} },
		withPrompt:  func(p string) []string { return []string{"codex", "exec", p} },
		defaults:    []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "model_reasoning_effort=high", "-m", "gpt-5-codex"},
		interactiveDefaults: []string{"--search"},
	},
	"aider": {
		interactive: func() []string { return []string{"aider"} },
		withPrompt:  func(p string) []string { return []string{"aider", "--message", p} },
		defaults:    []string{"--yes-always"},
	},
	"claude": {
		interactive: func() []string { return []string{"claude"} },
		withPrompt:  func(p string) []string { return []string{"claude", "-p", p} },
		defaults:    []string{"--dangerously-skip-permissions"},
	},
	"gemini": {
		interactive: func() []string { return []string{"gemini"} },
		withPrompt:  func(p string) []string { return []string{"gemini", "-p", p} },
		defaults:    []string{"-y"},
	},
	"crush": {
		interactive: func() []string { return []string{"crush"} },
		withPrompt:  func(p string) []string { return []string{"crush", "--prompt", p} },
		defaults:    []string{},
	},
	"amp": {
		interactive: func() []string { return []string{"amp"} },
		withPrompt:  func(p string) []string { return []string{"amp", "-x", p} },
		defaults:    []string{"--dangerously-allow-all"},
	},
	"opencode": {
		interactive: func() []string { return []string{"opencode"} },
		withPrompt:  func(p string) []string { return []string{"opencode", "run", p} },
		defaults:    []string{},
	},
}

// shellJoin quotes argv for safe execution in a single shell command.
func shellJoin(argv []string) string {
	quoted := make([]string, 0, len(argv))
	for _, a := range argv {
		quoted = append(quoted, shellQuote(a))
	}
	return strings.Join(quoted, " ")
}

// withUserPaths prefixes a shell command with PATH extensions for common user-level bin dirs.
func withUserPaths(cmd string) string {
	prefix := "export PATH=\"$HOME/.local/bin:$HOME/bin:$HOME/.npm-global/bin:$HOME/.cargo/bin:$PATH\"; "
	return prefix + cmd
}

// ---- Minimal TUI for multiline prompt entry ----

type promptModel struct {
	ta       textarea.Model
	canceled bool
	width    int
	height   int
}

func newPromptModel() promptModel {
	ta := textarea.New()
	ta.Prompt = ""
	ta.Placeholder = "Type your prompt..."
	ta.ShowLineNumbers = false
	ta.Focus()
	w, h, ok := measureTerminal()
	if !ok || w <= 0 {
		w = 80
	}
	if !ok || h <= 0 {
		h = 24
	}
	pad := 6
	tw := w - pad
	if tw < 20 {
		tw = 20
	}
	th := h - pad
	if th > 20 {
		th = 20
	}
	ta.SetWidth(tw)
	ta.SetHeight(th)
	return promptModel{ta: ta, width: w, height: h}
}

func (m promptModel) Init() tea.Cmd { return nil }

func (m promptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch t := msg.(type) {
	case tea.KeyMsg:
		switch t.Type {
		case tea.KeyEsc:
			m.canceled = true
			return m, tea.Quit
		case tea.KeyCtrlD, tea.KeyCtrlS:
			// Submit
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m promptModel) View() string {
	hint := "Ctrl+D to run • Esc to cancel"
	box := lipBox(m.ta.View()+"\n"+hint, m.width, m.height)
	return box
}

func lipBox(content string, termW, termH int) string {
	// Simple centered box without importing lipgloss here; reuse width sensibly.
	// Just return content; surrounding TUI already uses alt screen.
	return content
}

// isHelpArgs returns true when args are a simple help request like --help or -h
func isHelpArgs(args []string) bool {
	if len(args) == 1 {
		a := args[0]
		return a == "--help" || a == "-h" || a == "help"
	}
	return false
}

func init() {
	runAgentCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
}
