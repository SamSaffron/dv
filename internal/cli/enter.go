package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/docker"
)

var enterCmd = &cobra.Command{
	Use:   "enter [--name NAME]",
	Short: "Enter the container as 'discourse' (use --root for root)",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmdArgs := extractCommandArgs(args)
		if len(cmdArgs) > 0 {
			return fmt.Errorf("dv enter only opens a shell now; use 'dv run -- %s' to run commands inside the container", strings.Join(cmdArgs, " "))
		}

		ctx, ok, err := prepareContainerExecContext(cmd)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}

		execArgs := []string{"bash", "-l"}

		asRoot, _ := cmd.Flags().GetBool("root")
		if asRoot {
			return docker.ExecInteractiveAsRoot(ctx.name, ctx.workdir, ctx.envs, execArgs)
		}
		return docker.ExecInteractive(ctx.name, ctx.workdir, ctx.envs, execArgs)
	},
}

func init() {
	enterCmd.Flags().String("name", "", "Container name (defaults to selected or default)")
	enterCmd.Flags().Bool("root", false, "Enter as root user")
}
