package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
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

		// Get the port to expose
		portOverride, _ := cmd.Flags().GetInt("port")
		var port int
		if portOverride > 0 {
			port = portOverride
		} else {
			// Get the container's mapped port
			var err error
			port, err = getContainerPort(name)
			if err != nil {
				return fmt.Errorf("failed to get container port: %w", err)
			}
		}

		// Get all non-localhost network interfaces
		ips, err := getNonLocalhostIPs()
		if err != nil {
			return err
		}
		if len(ips) == 0 {
			return fmt.Errorf("no non-localhost network interfaces found")
		}

		// Find an available port for all interfaces
		availablePort, err := findAvailablePort(ips, port)
		if err != nil {
			return fmt.Errorf("failed to find available port: %w", err)
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
				if err := startProxy(ctx, ip, availablePort, port); err != nil {
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
}

// getContainerPort retrieves the host port that the container is mapped to
func getContainerPort(name string) (int, error) {
	out, err := docker.ExecOutput(name, "/", []string{"sh", "-c", "echo $DISCOURSE_PORT"})
	if err != nil {
		return 0, err
	}
	portStr := strings.TrimSpace(out)
	if portStr == "" {
		return 0, fmt.Errorf("DISCOURSE_PORT not set in container")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, fmt.Errorf("invalid DISCOURSE_PORT value: %s", portStr)
	}
	return port, nil
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
// If the preferred port is available on all IPs, it returns that port.
// Otherwise, it tries ports starting from the preferred port + 1.
func findAvailablePort(ips []string, preferredPort int) (int, error) {
	const maxAttempts = 100
	for port := preferredPort; port < preferredPort+maxAttempts; port++ {
		allAvailable := true
		for _, ip := range ips {
			addr := fmt.Sprintf("%s:%d", ip, port)
			listener, err := net.Listen("tcp", addr)
			if err != nil {
				allAvailable = false
				break
			}
			listener.Close()
		}
		if allAvailable {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available port found after %d attempts starting from %d", maxAttempts, preferredPort)
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

// startProxy starts a TCP proxy from listenIP:listenPort to localhost:targetPort
func startProxy(ctx context.Context, listenIP string, listenPort int, targetPort int) error {
	addr := fmt.Sprintf("%s:%d", listenIP, listenPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()

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

		go handleConnection(ctx, conn, targetPort)
	}
}

// handleConnection proxies a connection to localhost:targetPort
func handleConnection(ctx context.Context, clientConn net.Conn, targetPort int) {
	defer clientConn.Close()

	// Connect to localhost
	targetAddr := fmt.Sprintf("localhost:%d", targetPort)
	serverConn, err := net.Dial("tcp", targetAddr)
	if err != nil {
		return
	}
	defer serverConn.Close()

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
}
