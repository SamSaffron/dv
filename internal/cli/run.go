package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"dv/internal/docker"
)

var runCmd = &cobra.Command{
	Use:   "run [--name NAME] [--root] -- CMD [ARGS...]",
	Short: "Run a command inside the container as 'discourse' (use --root for root)",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		execArgs := extractCommandArgs(args)
		if len(execArgs) == 0 {
			return fmt.Errorf("no command specified. Provide a command after '--' or use 'dv enter' for an interactive shell")
		}

		ctx, ok, err := prepareContainerExecContext(cmd)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}

		asRoot, _ := cmd.Flags().GetBool("root")
		if asRoot {
			return docker.ExecInteractiveAsRoot(ctx.name, ctx.workdir, ctx.envs, execArgs)
		}
		return docker.ExecInteractive(ctx.name, ctx.workdir, ctx.envs, execArgs)
	},
}

func init() {
	runCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
	runCmd.Flags().Bool("root", false, "Run as root user")
}

func extractCommandArgs(args []string) []string {
	for i, a := range args {
		if a == "--" {
			return args[i+1:]
		}
	}
	return args
}
