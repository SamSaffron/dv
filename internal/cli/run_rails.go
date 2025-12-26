package cli

import (
	"dv/internal/docker"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// runRailsCmd implements `dv rails-runner` (alias: `rr`).
// Usage:
//
//	 dv rr <command|rb_file>
//	 dv rails-runner
//
// If no arguments are given, an interactive Rails console is started.
var runRailsCmd = &cobra.Command{
	Use:     "rails-runner [COMMAND|RB_FILE]",
	Aliases: []string{"rr"},
	Short:   "Run a Rails command, ruby script or console inside the Discourse container",
	Args:    cobra.MaximumNArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			if strings.HasPrefix(toComplete, "./") || strings.HasPrefix(toComplete, "../") || strings.HasPrefix(toComplete, "/") || strings.Contains(toComplete, string(os.PathSeparator)) {
				return nil, cobra.ShellCompDirectiveDefault
			}
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, ok, err := prepareContainerExecContext(cmd)

		if err != nil {
			return err
		}
		if !ok {
			return nil
		}

		commands, needsToCopy := buildRailsCommand(args)

		if needsToCopy {
			arg := args[0]
			src := filepath.Join(".", arg)

			err := docker.CopyToContainerWithOwnership(ctx.name, src, "/tmp/"+filepath.Base(arg), false)
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Error copying file to container: %v\n", err)
				return err
			}
		}

		return docker.ExecInteractive(ctx.name, ctx.workdir, ctx.envs,
			commands,
		)
	},
}

func buildRailsCommand(args []string) ([]string, bool) {
	if len(args) == 0 {
		return []string{"bundle", "exec", "rails", "console"}, false
	}

	arg := args[0]
	if strings.HasSuffix(arg, ".rb") {
		return []string{"bundle", "exec", "rails", "runner", "/tmp/" + filepath.Base(arg)}, true

	}

	return []string{"bundle", "exec", "rails", "runner", arg}, false
}
