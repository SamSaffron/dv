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
    zshInstall bool
)

var completionZshCmd = &cobra.Command{
    Use:   "zsh",
    Short: "Generate zsh completion script",
    Long:  "Generate zsh completion script. Use --install to install into your user site-functions directory.",
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
