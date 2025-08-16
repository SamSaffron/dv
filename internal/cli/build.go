package cli

import (
    "fmt"

    "github.com/spf13/cobra"

    "dv/internal/assets"
    "dv/internal/config"
    "dv/internal/docker"
    "dv/internal/xdg"
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build the Docker image",
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil { return err }
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil { return err }

		noCache, _ := cmd.Flags().GetBool("no-cache")
		buildArgs, _ := cmd.Flags().GetStringArray("build-arg")
		removeExisting, _ := cmd.Flags().GetBool("rm-existing")

		pass := make([]string, 0, len(buildArgs)+1)
		if noCache { pass = append(pass, "--no-cache") }
		for _, kv := range buildArgs { pass = append(pass, "--build-arg", kv) }

		if removeExisting && docker.Exists(cfg.DefaultContainer) {
			fmt.Fprintf(cmd.OutOrStdout(), "Removing existing container %s...\n", cfg.DefaultContainer)
			_ = docker.Stop(cfg.DefaultContainer)
			_ = docker.Remove(cfg.DefaultContainer)
		}
        dockerfilePath, contextDir, overridden, err := assets.ResolveDockerfile(configDir)
        if err != nil { return err }
        if overridden {
            fmt.Fprintf(cmd.OutOrStdout(), "Using override Dockerfile: %s\n", dockerfilePath)
        } else {
            fmt.Fprintf(cmd.OutOrStdout(), "Using embedded Dockerfile (sha=%s) at: %s\n", assets.EmbeddedDockerfileSHA256()[:12], dockerfilePath)
        }

        fmt.Fprintf(cmd.OutOrStdout(), "Building Docker image as: %s\n", cfg.ImageTag)
        if err := docker.BuildFrom(cfg.ImageTag, dockerfilePath, contextDir, pass); err != nil { return err }
		fmt.Fprintln(cmd.OutOrStdout(), "Done.")
		return nil
	},
}

func init() {
	buildCmd.Flags().Bool("no-cache", false, "Do not use cache when building the image")
	buildCmd.Flags().StringArray("build-arg", nil, "Set build-time variables (KEY=VALUE)")
	buildCmd.Flags().Bool("rm-existing", false, "Remove existing default container before building")
}
