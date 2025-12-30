package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var exposeCmd = &cobra.Command{
	Use:   "expose [--port PORT]",
	Short: "Expose container on public network interfaces for testing",
	Long: `Expose the container on all non-localhost network interfaces.
This allows you to access the container from other devices on your local network (e.g., iPhone).
Press Ctrl+C to stop exposing.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		verbose, _ := cmd.Flags().GetBool("verbose")
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		name := currentAgentName(cfg)
		if verbose {
			fmt.Fprintf(cmd.OutOrStdout(), "[verbose] Selected container: %s\n", name)
		}
		if !docker.Running(name) {
			return fmt.Errorf("container '%s' is not running; start it with 'dv start'", name)
		}
		if verbose {
			fmt.Fprintf(cmd.OutOrStdout(), "[verbose] Container '%s' is running\n", name)
		}

		// Get the container target (IP and port)
		portOverride, _ := cmd.Flags().GetInt("port")
		var targetAddr string
		if portOverride > 0 {
			// If port override, assume localhost
			targetAddr = fmt.Sprintf("localhost:%d", portOverride)
			if verbose {
				fmt.Fprintf(cmd.OutOrStdout(), "[verbose] Using port override, target: %s\n", targetAddr)
			}
		} else {
			// Get the container's IP and port directly
			if verbose {
				fmt.Fprintln(cmd.OutOrStdout(), "[verbose] Querying container target...")
			}
			containerIP, containerPort, err := getContainerTarget(name, verbose, cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("failed to get container target: %w", err)
			}
			targetAddr = fmt.Sprintf("%s:%d", containerIP, containerPort)
			if verbose {
				fmt.Fprintf(cmd.OutOrStdout(), "[verbose] Will proxy to %s\n", targetAddr)
			}
		}

		// Get all non-localhost network interfaces
		if verbose {
			fmt.Fprintln(cmd.OutOrStdout(), "[verbose] Scanning network interfaces...")
		}
		ips, err := getNonLocalhostIPs()
		if err != nil {
			return err
		}
		if len(ips) == 0 {
			return fmt.Errorf("no non-localhost network interfaces found")
		}
		if verbose {
			fmt.Fprintf(cmd.OutOrStdout(), "[verbose] Found %d non-localhost IP(s):\n", len(ips))
			for _, ip := range ips {
				ifaceName := getInterfaceName(ip)
				if ifaceName != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "[verbose]   %s (%s)\n", ip, ifaceName)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "[verbose]   %s\n", ip)
				}
			}
		}

		// Find an available port for all interfaces
		if verbose {
			fmt.Fprintln(cmd.OutOrStdout(), "[verbose] Searching for available port starting from 10000...")
		}
		availablePort, err := findAvailablePort(ips, verbose, cmd.OutOrStdout())
		if err != nil {
			return fmt.Errorf("failed to find available port: %w", err)
		}
		if verbose {
			fmt.Fprintf(cmd.OutOrStdout(), "[verbose] Found available port: %d\n", availablePort)
			fmt.Fprintf(cmd.OutOrStdout(), "[verbose] Will proxy %s:%d -> %s\n", ips[0], availablePort, targetAddr)
		}

		// Start proxies for each IP
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var wg sync.WaitGroup
		errChan := make(chan error, len(ips))

		for _, ip := range ips {
			wg.Add(1)
			go func(ip string) {
				defer wg.Done()
				if verbose {
					fmt.Fprintf(cmd.OutOrStdout(), "[verbose] Starting proxy on %s:%d\n", ip, availablePort)
				}
				if err := startProxy(ctx, ip, availablePort, targetAddr, verbose, cmd.OutOrStdout()); err != nil {
					errChan <- fmt.Errorf("proxy on %s: %w", ip, err)
				}
			}(ip)
		}

		// Display success message
		fmt.Fprintln(cmd.OutOrStdout(), "✓ Container exposed on local network")
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintln(cmd.OutOrStdout(), "  From your device, visit:")
		for _, ip := range ips {
			ifaceName := getInterfaceName(ip)
			fmt.Fprintf(cmd.OutOrStdout(), "  http://%s:%d", ip, availablePort)
			if ifaceName != "" {
				fmt.Fprintf(cmd.OutOrStdout(), " (%s)", ifaceName)
			}
			fmt.Fprintln(cmd.OutOrStdout())
		}
		fmt.Fprintln(cmd.OutOrStdout())
		fmt.Fprintln(cmd.OutOrStdout(), "  Press Ctrl+C to stop")

		// Wait for interrupt signal
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

		select {
		case <-sigChan:
			fmt.Fprintln(cmd.OutOrStdout())
			fmt.Fprintln(cmd.OutOrStdout(), "Stopping...")
			cancel()
		case err := <-errChan:
			cancel()
			wg.Wait()
			return err
		}

		wg.Wait()
		return nil
	},
}

func init() {
	exposeCmd.Flags().Int("port", 0, "Port to expose (defaults to container's mapped port)")
	exposeCmd.Flags().Bool("verbose", false, "Show detailed debugging information")
}

// getContainerTarget returns the container's IP and internal port to connect to
func getContainerTarget(name string, verbose bool, out io.Writer) (string, int, error) {
	// Get the container's IP address
	containerIP, err := docker.ContainerIP(name)
	if err != nil {
		return "", 0, fmt.Errorf("failed to get container IP: %w", err)
	}
	if verbose {
		fmt.Fprintf(out, "[verbose] Container IP: %s\n", containerIP)
	}

	// Default to port 443 (standard HTTPS port used by Discourse in container)
	containerPort := 443
	if verbose {
		fmt.Fprintf(out, "[verbose] Container port: %d\n", containerPort)
	}

	return containerIP, containerPort, nil
}

// getNonLocalhostIPs returns all IPv4 addresses that are not localhost
func getNonLocalhostIPs() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var ips []string
	for _, iface := range ifaces {
		// Skip down interfaces
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		// Skip loopback
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			// Only IPv4, not loopback
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				continue
			}

			ips = append(ips, ip.String())
		}
	}

	return ips, nil
}

// findAvailablePort finds a port that's available on all given IPs
// Starts from port 10000 to avoid privileged ports and common conflicts.
func findAvailablePort(ips []string, verbose bool, out io.Writer) (int, error) {
	const startPort = 10000
	const maxAttempts = 100
	for port := startPort; port < startPort+maxAttempts; port++ {
		allAvailable := true
		for _, ip := range ips {
			addr := fmt.Sprintf("%s:%d", ip, port)
			listener, err := net.Listen("tcp", addr)
			if err != nil {
				if verbose {
					fmt.Fprintf(out, "[verbose] Port %d unavailable on %s: %v\n", port, ip, err)
				}
				allAvailable = false
				break
			}
			listener.Close()
		}
		if allAvailable {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available port found after %d attempts starting from %d", maxAttempts, startPort)
}

// getInterfaceName returns a friendly name for the interface with the given IP
func getInterfaceName(targetIP string) string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip != nil && ip.String() == targetIP {
				// Return a friendly name based on common patterns
				name := iface.Name
				if strings.HasPrefix(name, "en") {
					return "Wi-Fi"
				} else if strings.HasPrefix(name, "eth") {
					return "Ethernet"
				}
				return name
			}
		}
	}

	return ""
}

// startProxy starts a TCP proxy from listenIP:listenPort to targetAddr
func startProxy(ctx context.Context, listenIP string, listenPort int, targetAddr string, verbose bool, out io.Writer) error {
	addr := fmt.Sprintf("%s:%d", listenIP, listenPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	if verbose {
		fmt.Fprintf(out, "[verbose] Listener started on %s\n", addr)
	}

	// Channel to signal listener is closed
	done := make(chan struct{})

	go func() {
		<-ctx.Done()
		listener.Close()
		close(done)
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}

		if verbose {
			fmt.Fprintf(out, "[verbose] Accepted connection from %s\n", conn.RemoteAddr())
		}
		go handleConnection(ctx, conn, targetAddr, verbose, out)
	}
}

// handleConnection proxies a connection to targetAddr
func handleConnection(ctx context.Context, clientConn net.Conn, targetAddr string, verbose bool, out io.Writer) {
	defer clientConn.Close()

	serverConn, err := net.Dial("tcp", targetAddr)
	if err != nil {
		if verbose {
			fmt.Fprintf(out, "[verbose] Failed to connect to %s: %v\n", targetAddr, err)
		}
		return
	}
	defer serverConn.Close()

	if verbose {
		fmt.Fprintf(out, "[verbose] Proxying %s <-> %s\n", clientConn.RemoteAddr(), targetAddr)
	}

	// Bidirectional copy
	done := make(chan struct{})

	go func() {
		io.Copy(serverConn, clientConn)
		done <- struct{}{}
	}()

	go func() {
		io.Copy(clientConn, serverConn)
		done <- struct{}{}
	}()

	// Wait for context cancellation or connection to close
	select {
	case <-ctx.Done():
	case <-done:
	}

	if verbose {
		fmt.Fprintf(out, "[verbose] Connection closed: %s\n", clientConn.RemoteAddr())
	}
}
