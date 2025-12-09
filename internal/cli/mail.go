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

var mailCmd = &cobra.Command{
	Use:   "mail [--port PORT] [--host-port HOST_PORT]",
	Short: "Run MailHog and tunnel it to localhost",
	Long: `Start MailHog in the container and create a tunnel to localhost.
This allows you to access MailHog from your browser without reconfiguring Docker.
Press Ctrl+C to stop both MailHog and the tunnel.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		verbose, _ := cmd.Flags().GetBool("verbose")
		log := func(format string, a ...any) {
			if verbose {
				fmt.Fprintf(cmd.OutOrStdout(), "[debug] "+format+"\n", a...)
			}
		}

		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		name := currentAgentName(cfg)
		log("Using container: %s", name)
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
		log("Container port: %d, Host port: %d", containerPort, hostPort)

		// Start MailHog in the container as discourse user
		log("Starting MailHog process: docker exec -u discourse %s mailhog", name)
		mailhogProcess := exec.Command("docker", "exec", "-u", "discourse", name, "mailhog")
		mailhogProcess.Stdout = nil
		mailhogProcess.Stderr = os.Stderr
		if err := mailhogProcess.Start(); err != nil {
			return fmt.Errorf("failed to start MailHog: %w", err)
		}
		log("MailHog started with PID: %d", mailhogProcess.Process.Pid)

		// Ensure MailHog is killed on exit (must kill inside container since docker exec doesn't forward signals)
		defer func() {
			log("Cleanup: killing mailhog inside container")
			killCmd := exec.Command("docker", "exec", name, "pkill", "-f", "mailhog")
			if err := killCmd.Run(); err != nil {
				log("pkill mailhog returned: %v", err)
			} else {
				log("pkill mailhog succeeded")
			}
			if mailhogProcess.Process != nil {
				log("Cleanup: killing docker exec process PID %d", mailhogProcess.Process.Pid)
				mailhogProcess.Process.Kill()
				mailhogProcess.Wait()
				log("docker exec process terminated")
			}
		}()

		fmt.Fprintln(cmd.OutOrStdout(), "Starting MailHog...")

		// Start socat tunnel
		socatArgs := []string{
			fmt.Sprintf("TCP-LISTEN:%d,fork,reuseaddr,bind=127.0.0.1", hostPort),
			fmt.Sprintf("EXEC:docker exec -i %s socat STDIO TCP\\:localhost\\:%d", name, containerPort),
		}
		log("Starting socat tunnel: socat %s %s", socatArgs[0], socatArgs[1])
		socatProcess := exec.Command("socat", socatArgs...)
		socatProcess.Stdout = os.Stdout
		socatProcess.Stderr = os.Stderr
		if err := socatProcess.Start(); err != nil {
			return fmt.Errorf("failed to start socat tunnel: %w", err)
		}
		log("socat started with PID: %d", socatProcess.Process.Pid)

		// Ensure socat is killed on exit
		defer func() {
			if socatProcess.Process != nil {
				log("Cleanup: killing socat process PID %d", socatProcess.Process.Pid)
				socatProcess.Process.Kill()
				socatProcess.Wait()
				log("socat process terminated")
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
			log("Waiting for mailhog process to exit...")
			mailhogProcess.Wait()
			log("mailhog process exited")
			close(doneChan)
		}()

		select {
		case sig := <-sigChan:
			log("Received signal: %v", sig)
			fmt.Fprintln(cmd.OutOrStdout())
			fmt.Fprintln(cmd.OutOrStdout(), "Stopping MailHog and tunnel...")
		case <-doneChan:
			log("MailHog process exited on its own")
			fmt.Fprintln(cmd.OutOrStdout(), "MailHog exited")
		}

		log("Exiting RunE, defers will run")
		return nil
	},
}

func init() {
	mailCmd.Flags().Int("port", 8025, "MailHog port inside the container")
	mailCmd.Flags().Int("host-port", 8025, "Port to expose on localhost (defaults to same as --port)")
	mailCmd.Flags().BoolP("verbose", "V", false, "Enable verbose debug output")
}
