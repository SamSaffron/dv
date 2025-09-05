package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion",
	Short: "Generate shell completion scripts",
	Long:  "Generate shell completion scripts for dv. Use 'dv config completion zsh' to print the zsh completion script.",
}

var (
	zshInstall  bool
	bashInstall bool
)

var completionZshCmd = &cobra.Command{
	Use:   "zsh",
	Short: "Generate zsh completion script",
	Long:  "Generate zsh completion script. Use --install to install into your user site-functions directory.",
	Args:  cobra.NoArgs,
	// No positional args; show flags on TAB instead of files
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if zshInstall {
			return installZshCompletion(cmd)
		}
		return rootCmd.GenZshCompletion(cmd.OutOrStdout())
	},
}

var completionZshInstallCmd = &cobra.Command{
	Use:   "zsh-install",
	Short: "Install zsh completion for current user",
	Long: `Install zsh completion for the current user. This command:
- Installs the completion script to ~/.local/share/zsh/site-functions/_dv
- Intelligently updates ~/.zshrc to load completions without duplication
- Is safe to run multiple times without growing your .zshrc file
- Provides clear instructions on what was done`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return installZshCompletionSmart(cmd)
	},
}

func init() {
	completionZshCmd.Flags().BoolVar(&zshInstall, "install", false, "Install completion into ~/.local/share/zsh/site-functions/_dv")
	completionCmd.AddCommand(completionZshCmd)
	completionCmd.AddCommand(completionZshInstallCmd)
	// Bash completion subcommand
	completionBashCmd.Flags().BoolVar(&bashInstall, "install", false, "Install completion into ~/.local/share/bash-completion/completions/dv")
	completionCmd.AddCommand(completionBashCmd)
	// Move completion under config
	configCmd.AddCommand(completionCmd)
}

func installZshCompletion(cmd *cobra.Command) error {
	dir, err := zshSiteFunctionsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "_dv")
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func(c io.Closer) { _ = c.Close() }(file)
	if err := rootCmd.GenZshCompletion(file); err != nil {
		return err
	}

	// Provide brief next steps to the user
	fmt.Fprintf(cmd.OutOrStdout(), "Installed zsh completion to %s\n", path)
	fmt.Fprintln(cmd.OutOrStdout(), "If zsh does not find it, add the following to your ~/.zshrc before 'compinit':")
	fmt.Fprintf(cmd.OutOrStdout(), "\n  fpath+=(%s)\n  autoload -U compinit\n  compinit\n\n", dir)
	return nil
}

var completionBashCmd = &cobra.Command{
	Use:   "bash",
	Short: "Generate bash completion script",
	Long:  "Generate bash completion script. Use --install to install into your user bash-completion directory.",
	Args:  cobra.NoArgs,
	// No positional args; show flags on TAB instead of files
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if bashInstall {
			return installBashCompletion(cmd)
		}
		// Use V2 which avoids bashcompinit requirements
		return rootCmd.GenBashCompletionV2(cmd.OutOrStdout(), true)
	},
}

func installBashCompletion(cmd *cobra.Command) error {
	dir, err := bashCompletionsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "dv")
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func(c io.Closer) { _ = c.Close() }(file)
	if err := rootCmd.GenBashCompletionV2(file, true); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Installed bash completion to %s\n", path)
	fmt.Fprintln(cmd.OutOrStdout(), "If completion does not work, ensure bash-completion is enabled (package installed) and add this to your ~/.bashrc if needed:")
	fmt.Fprintf(cmd.OutOrStdout(), "\n  # Load bash-completion if present\n  if [ -f /usr/share/bash-completion/bash_completion ]; then\n    . /usr/share/bash-completion/bash_completion\n  elif [ -f /etc/bash_completion ]; then\n    . /etc/bash_completion\n  fi\n\n")
	return nil
}

func zshSiteFunctionsDir() (string, error) {
	if v := os.Getenv("ZSH_COMPLETIONS_DIR"); v != "" {
		return v, nil
	}
	if v := os.Getenv("ZSH_SITE_FUNCTIONS"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "zsh", "site-functions"), nil
}

func bashCompletionsDir() (string, error) {
	if v := os.Getenv("BASH_COMPLETIONS_DIR"); v != "" {
		return v, nil
	}
	// Follow XDG if set, otherwise default to ~/.local/share
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "bash-completion", "completions"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "bash-completion", "completions"), nil
}

func installZshCompletionSmart(cmd *cobra.Command) error {
	// Step 1: Install the completion script
	dir, err := zshSiteFunctionsDir()
	if err != nil {
		return fmt.Errorf("failed to get zsh site functions directory: %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	path := filepath.Join(dir, "_dv")
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create completion file %s: %w", path, err)
	}
	defer func(c io.Closer) { _ = c.Close() }(file)

	if err := rootCmd.GenZshCompletion(file); err != nil {
		return fmt.Errorf("failed to generate zsh completion: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ Installed zsh completion script to %s\n", path)

	// Step 2: Update .zshrc intelligently
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	zshrcPath := filepath.Join(home, ".zshrc")

	// Check if .zshrc exists
	zshrcExists := true
	if _, err := os.Stat(zshrcPath); os.IsNotExist(err) {
		zshrcExists = false
	}

	// Read existing .zshrc content
	var existingLines []string
	if zshrcExists {
		file, err := os.Open(zshrcPath)
		if err != nil {
			return fmt.Errorf("failed to read existing .zshrc: %w", err)
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			existingLines = append(existingLines, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("failed to read .zshrc: %w", err)
		}
	}

	// Check if dv completion is already configured
	dvCompletionMarker := "# dv completion setup"
	hasDvCompletion := false

	for _, line := range existingLines {
		if strings.Contains(line, dvCompletionMarker) {
			hasDvCompletion = true
			break
		}
	}

	// Prepare the completion setup block
	completionBlock := []string{
		"",
		dvCompletionMarker,
		"fpath+=(" + dir + ")",
		"autoload -U compinit",
		"compinit",
		"",
	}

	var newLines []string
	modified := false

	if hasDvCompletion {
		// Replace existing dv completion block
		inDvBlock := false
		for _, line := range existingLines {
			if strings.Contains(line, dvCompletionMarker) {
				inDvBlock = true
				// Add the new block
				newLines = append(newLines, completionBlock...)
				continue
			}
			if inDvBlock {
				// Skip lines until we find the end of the block (empty line or next section)
				if strings.TrimSpace(line) == "" {
					inDvBlock = false
					newLines = append(newLines, line)
				}
				continue
			}
			newLines = append(newLines, line)
		}
		modified = true
		fmt.Fprintf(cmd.OutOrStdout(), "✓ Updated existing dv completion configuration in .zshrc\n")
	} else {
		// Add new completion block
		newLines = append(existingLines, completionBlock...)
		modified = true
		fmt.Fprintf(cmd.OutOrStdout(), "✓ Added dv completion configuration to .zshrc\n")
	}

	// Write the updated .zshrc
	if modified {
		file, err := os.Create(zshrcPath)
		if err != nil {
			return fmt.Errorf("failed to write .zshrc: %w", err)
		}
		defer file.Close()

		for _, line := range newLines {
			if _, err := fmt.Fprintln(file, line); err != nil {
				return fmt.Errorf("failed to write line to .zshrc: %w", err)
			}
		}
	}

	// Provide summary
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Installation complete! Here's what was done:")
	fmt.Fprintf(cmd.OutOrStdout(), "• Completion script installed to: %s\n", path)
	fmt.Fprintf(cmd.OutOrStdout(), "• .zshrc updated to load completions from: %s\n", dir)
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "To activate completions:")
	fmt.Fprintln(cmd.OutOrStdout(), "• Restart your terminal, or")
	fmt.Fprintln(cmd.OutOrStdout(), "• Run: source ~/.zshrc")
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "You can safely run this command again - it won't duplicate entries.")

	return nil
}
