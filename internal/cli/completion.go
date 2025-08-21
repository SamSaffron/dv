package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion",
	Short: "Generate shell completion scripts",
	Long:  "Generate shell completion scripts for dv. Use 'dv completion zsh' to print the zsh completion script.",
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

func init() {
	completionZshCmd.Flags().BoolVar(&zshInstall, "install", false, "Install completion into ~/.local/share/zsh/site-functions/_dv")
	completionCmd.AddCommand(completionZshCmd)
	// Bash completion subcommand
	completionBashCmd.Flags().BoolVar(&bashInstall, "install", false, "Install completion into ~/.local/share/bash-completion/completions/dv")
	completionCmd.AddCommand(completionBashCmd)
	rootCmd.AddCommand(completionCmd)
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
		// Prefer V2 which avoids deprecated bashcompinit requirements
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
