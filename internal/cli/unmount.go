package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"dv/internal/xdg"
)

var unmountCmd = &cobra.Command{
	Use:   "unmount <mount-name>",
	Short: "Unmount a previously mounted directory",
	Long: `Unmount a directory that was previously mounted via SSHFS.

The mount name should be the sanitized name used when mounting (without the full path).

Examples:
  dv unmount var_www_discourse
  dv unmount home_discourse_projects`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mountName := args[0]

		// Get data directory
		dataDir, err := xdg.DataDir()
		if err != nil {
			return err
		}

		// Construct mount point path
		mountPoint := filepath.Join(dataDir, "mounts", mountName)

		// Check if mount point exists
		if _, err := os.Stat(mountPoint); os.IsNotExist(err) {
			return fmt.Errorf("mount point '%s' does not exist", mountPoint)
		}

		// Check if it's actually mounted
		if !isMounted(mountPoint) {
			fmt.Fprintf(cmd.OutOrStdout(), "Mount point '%s' is not currently mounted\n", mountPoint)
			return nil
		}

		// Unmount using fusermount (Linux) or umount (macOS)
		var unmountCmd *exec.Cmd
		switch runtime.GOOS {
		case "linux":
			unmountCmd = exec.Command("fusermount", "-u", mountPoint)
		case "darwin":
			unmountCmd = exec.Command("umount", mountPoint)
		default:
			return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
		}

		output, err := unmountCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to unmount: %s", string(output))
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Successfully unmounted '%s'\n", mountPoint)
		return nil
	},
}
