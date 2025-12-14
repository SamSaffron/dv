package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/localproxy"
	"dv/internal/xdg"
)

var configLocalProxyCmd = &cobra.Command{
	Use:   "local-proxy",
	Short: "Run a local HTTP proxy so containers are reachable via NAME.localhost",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		lp := cfg.LocalProxy
		lp.ApplyDefaults()

		nameFlag, _ := cmd.Flags().GetString("name")
		imageFlag, _ := cmd.Flags().GetString("image")
		httpPortFlag, _ := cmd.Flags().GetInt("http-port")
		apiPortFlag, _ := cmd.Flags().GetInt("api-port")
		rebuild, _ := cmd.Flags().GetBool("rebuild")
		recreate, _ := cmd.Flags().GetBool("recreate")
		public, _ := cmd.Flags().GetBool("public")

		if name := trimFlag(nameFlag); name != "" {
			lp.ContainerName = name
		}
		if img := trimFlag(imageFlag); img != "" {
			lp.ImageTag = img
		}
		if httpPortFlag > 0 {
			lp.HTTPPort = httpPortFlag
		}
		if apiPortFlag > 0 {
			lp.APIPort = apiPortFlag
		}
		lp.ApplyDefaults()

		if lp.HTTPPort == lp.APIPort {
			return fmt.Errorf("http-port and api-port must differ")
		}

		if rebuild || !docker.ImageExists(lp.ImageTag) {
			fmt.Fprintf(cmd.OutOrStdout(), "Building local proxy image '%s'...\n", lp.ImageTag)
			if err := localproxy.BuildImage(configDir, lp); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Reusing existing image '%s'.\n", lp.ImageTag)
		}

		if err := localproxy.EnsureContainer(lp, recreate, public); err != nil {
			return err
		}
		if err := localproxy.Healthy(lp, 5*time.Second); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %v\n", err)
		}

		lp.Enabled = true
		cfg.LocalProxy = lp
		if err := config.Save(configDir, cfg); err != nil {
			return err
		}

		if public {
			fmt.Fprintf(cmd.OutOrStdout(), "Local proxy '%s' is ready on port %d (public); API on %d (public).\n", lp.ContainerName, lp.HTTPPort, lp.APIPort)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Local proxy '%s' is ready on port %d (localhost only); API on %d (localhost only).\n", lp.ContainerName, lp.HTTPPort, lp.APIPort)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "New containers will register as NAME.localhost when this proxy is running. Remove the proxy container to stop using it.")
		return nil
	},
}

func init() {
	configLocalProxyCmd.Flags().String("name", "", "Container name to run the proxy as (default dv-local-proxy)")
	configLocalProxyCmd.Flags().String("image", "", "Image tag to build/use for the proxy (default dv-local-proxy)")
	configLocalProxyCmd.Flags().Int("http-port", 0, "Host port that will listen for NAME.localhost requests (defaults to 80)")
	configLocalProxyCmd.Flags().Int("api-port", 0, "Host port for the proxy management API")
	configLocalProxyCmd.Flags().Bool("rebuild", false, "Force rebuilding the proxy image even if it exists")
	configLocalProxyCmd.Flags().Bool("recreate", false, "Remove any existing proxy container before starting")
	configLocalProxyCmd.Flags().Bool("public", false, "Listen on all network interfaces (default: private/localhost only)")
	configCmd.AddCommand(configLocalProxyCmd)
}

func trimFlag(val string) string {
	return strings.TrimSpace(val)
}
