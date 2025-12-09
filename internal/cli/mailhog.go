package cli

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var mailhogCmd = &cobra.Command{
	Use:   "mailhog [--port PORT] [--host-port HOST_PORT]",
	Short: "Run MailHog and tunnel it to localhost",
	Long: `Start MailHog in the container and create a tunnel to localhost.
This allows you to access MailHog from your browser without reconfiguring Docker.
Press Ctrl+C to stop both MailHog and the tunnel.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		name := currentAgentName(cfg)
		if !docker.Running(name) {
			return fmt.Errorf("container '%s' is not running; start it with 'dv start'", name)
		}

		containerPort, _ := cmd.Flags().GetInt("port")
		if containerPort == 0 {
			containerPort = 8025
		}

		hostPort, _ := cmd.Flags().GetInt("host-port")
		if hostPort == 0 {
			hostPort = containerPort
		}

		// Start MailHog in the container as discourse user
		mailhogProcess := exec.Command("docker", "exec", "-u", "discourse", name, "mailhog")
		mailhogProcess.Stdout = nil
		mailhogProcess.Stderr = os.Stderr
		if err := mailhogProcess.Start(); err != nil {
			return fmt.Errorf("failed to start MailHog: %w", err)
		}

		// Ensure MailHog is killed on exit (must kill inside container since docker exec doesn't forward signals)
		defer func() {
			exec.Command("docker", "exec", name, "pkill", "-f", "mailhog").Run()
			if mailhogProcess.Process != nil {
				mailhogProcess.Process.Kill()
				mailhogProcess.Wait()
			}
		}()

		fmt.Fprintln(cmd.OutOrStdout(), "Starting MailHog...")

		// Start socat tunnel
		socatProcess := exec.Command(
			"socat",
			fmt.Sprintf("TCP-LISTEN:%d,fork,reuseaddr,bind=127.0.0.1", hostPort),
			fmt.Sprintf("EXEC:docker exec -i %s socat STDIO TCP\\:localhost\\:%d", name, containerPort),
		)
		socatProcess.Stdout = os.Stdout
		socatProcess.Stderr = os.Stderr
		if err := socatProcess.Start(); err != nil {
			return fmt.Errorf("failed to start socat tunnel: %w", err)
		}

		// Ensure socat is killed on exit
		defer func() {
			if socatProcess.Process != nil {
				socatProcess.Process.Kill()
				socatProcess.Wait()
			}
		}()

		fmt.Fprintln(cmd.OutOrStdout(), "✓ MailHog is running and tunneled to localhost")
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintf(cmd.OutOrStdout(), "  Open in your browser: http://localhost:%d\n", hostPort)
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintln(cmd.OutOrStdout(), "  Press Ctrl+C to stop")

		// Wait for interrupt signal or process exit
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigChan)

		doneChan := make(chan struct{})
		go func() {
			mailhogProcess.Wait()
			close(doneChan)
		}()

		select {
		case <-sigChan:
			fmt.Fprintln(cmd.OutOrStdout())
			fmt.Fprintln(cmd.OutOrStdout(), "Stopping MailHog and tunnel...")
		case <-doneChan:
			fmt.Fprintln(cmd.OutOrStdout(), "MailHog exited")
		}

		return nil
	},
}

func init() {
	mailhogCmd.Flags().Int("port", 8025, "MailHog port inside the container")
	mailhogCmd.Flags().Int("host-port", 8025, "Port to expose on localhost (defaults to same as --port)")
}
