package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"dv/internal/xdg"
)

var dataCmd = &cobra.Command{
	Use:   "data",
	Short: "Show data directory path",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := xdg.DataDir()
		if err != nil { return err }
		fmt.Fprintln(cmd.OutOrStdout(), dir)
		return nil
	},
}
