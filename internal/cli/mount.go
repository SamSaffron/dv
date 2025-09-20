package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var mountCmd = &cobra.Command{
	Use:   "mount <remote-path>",
	Short: "Mount a remote directory via SSHFS",
	Long: `Mount a remote directory from the container via SSHFS.

The mount will be created in the dv data directory with a sanitized name based on the remote path.
SSHFS must be installed on the host system.

Examples:
  dv mount /var/www/discourse
  dv mount /home/discourse/projects`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		remotePath := args[0]

		// Check if sshfs is installed
		if err := checkSSHFSInstalled(); err != nil {
			return err
		}

		// Load config
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		// Get current agent name
		agentName := currentAgentName(cfg)
		if agentName == "" {
			return fmt.Errorf("no agent selected")
		}

		// Check if container is running
		if !docker.Running(agentName) {
			return fmt.Errorf("container '%s' is not running", agentName)
		}

		// Get SSH port for the container
		sshPort, err := getContainerSSHPort(agentName)
		if err != nil {
			return fmt.Errorf("failed to get SSH port for container '%s': %w", agentName, err)
		}

		// Create mount point
		mountName := sanitizeMountName(remotePath)
		dataDir, err := xdg.DataDir()
		if err != nil {
			return err
		}
		mountPoint := filepath.Join(dataDir, "mounts", mountName)

		if err := os.MkdirAll(mountPoint, 0755); err != nil {
			return fmt.Errorf("failed to create mount point: %w", err)
		}

		// Check if already mounted
		if isMounted(mountPoint) {
			fmt.Fprintf(cmd.OutOrStdout(), "Mount point '%s' is already mounted\n", mountPoint)
			return nil
		}

		// Create SSHFS mount
		sshfsCmd := exec.Command("sshfs",
			fmt.Sprintf("discourse@localhost:%s", remotePath),
			mountPoint,
			"-p", fmt.Sprintf("%d", sshPort),
			"-o", "password_stdin",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
		)

		// Provide password via stdin
		sshfsCmd.Stdin = strings.NewReader("password\n")

		output, err := sshfsCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to mount: %s", string(output))
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Successfully mounted '%s' to '%s'\n", remotePath, mountPoint)
		fmt.Fprintf(cmd.OutOrStdout(), "SSH port: %d\n", sshPort)
		return nil
	},
}

func checkSSHFSInstalled() error {
	_, err := exec.LookPath("sshfs")
	if err != nil {
		var installCmd string
		switch runtime.GOOS {
		case "darwin":
			installCmd = "brew install macfuse sshfs"
		case "linux":
			installCmd = "sudo apt-get install sshfs  # or sudo yum install fuse-sshfs"
		default:
			installCmd = "install sshfs for your platform"
		}
		return fmt.Errorf("sshfs is not installed. Please install it first:\n  %s", installCmd)
	}
	return nil
}

func getContainerSSHPort(containerName string) (int, error) {
	// Get port mapping for SSH port (22) from the container
	cmd := exec.Command("docker", "port", containerName, "22")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to get port mapping: %s", string(output))
	}

	// Parse output like "0.0.0.0:20022" or "0.0.0.0:20022->22/tcp"
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return 0, fmt.Errorf("no SSH port mapping found")
	}

	// Take the first line and extract the port
	line := lines[0]

	// Handle both formats: "0.0.0.0:20022" and "0.0.0.0:20022->22/tcp"
	var portStr string
	if strings.Contains(line, "->") {
		// Format: "0.0.0.0:20022->22/tcp"
		parts := strings.Split(line, "->")
		if len(parts) < 2 {
			return 0, fmt.Errorf("invalid port mapping format")
		}
		hostPart := strings.Split(parts[0], ":")
		if len(hostPart) < 2 {
			return 0, fmt.Errorf("invalid host port format")
		}
		portStr = hostPart[1]
	} else {
		// Format: "0.0.0.0:20022"
		parts := strings.Split(line, ":")
		if len(parts) < 2 {
			return 0, fmt.Errorf("invalid port format")
		}
		portStr = parts[1]
	}

	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return 0, fmt.Errorf("failed to parse port '%s': %w", portStr, err)
	}

	return port, nil
}

func sanitizeMountName(remotePath string) string {
	// Replace path separators and other special characters with underscores
	name := strings.ReplaceAll(remotePath, "/", "_")
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")

	// Remove leading underscores
	name = strings.TrimLeft(name, "_")

	// Ensure it's not empty
	if name == "" {
		name = "root"
	}

	return name
}

func isMounted(mountPoint string) bool {
	// Check if the mount point is already mounted by looking at /proc/mounts (Linux) or mount (macOS)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("grep", "-q", mountPoint, "/proc/mounts")
	case "darwin":
		cmd = exec.Command("mount")
	default:
		return false
	}

	if cmd == nil {
		return false
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}

	if runtime.GOOS == "darwin" {
		// On macOS, check if the mount point appears in mount output
		return strings.Contains(string(output), mountPoint)
	}

	// On Linux, grep -q returns 0 if found, non-zero if not found
	return true
}
