package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/assets"
	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

var buildCmd = &cobra.Command{
	Use:   "build [TARGET]",
	Short: "Build a configured image, a stock image, or a Dockerfile path",
	Args:  cobra.RangeArgs(0, 1),
	// Complete TARGET with stock image names first; fall back to file completion when typing a path
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Only complete the first positional arg
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		// If the user is clearly starting a path, defer to filesystem completion
		if strings.HasPrefix(toComplete, "./") || strings.HasPrefix(toComplete, "../") || strings.HasPrefix(toComplete, "/") || strings.Contains(toComplete, string(os.PathSeparator)) {
			return nil, cobra.ShellCompDirectiveDefault
		}

		// Start with the two shipped stock images, then append any configured ones
		suggestions := []string{"discourse", "theme"}

		// Try to load configured images; ignore errors for completion purposes
		if configDir, err := xdg.ConfigDir(); err == nil {
			if cfg, err := config.LoadOrCreate(configDir); err == nil {
				for name := range cfg.Images {
					if name != "discourse" && name != "theme" {
						suggestions = append(suggestions, name)
					}
				}
			}
		}

		// Filter by prefix the shell is completing
		prefix := strings.ToLower(strings.TrimSpace(toComplete))
		filtered := make([]string, 0, len(suggestions))
		for _, s := range suggestions {
			if prefix == "" || strings.HasPrefix(strings.ToLower(s), prefix) {
				filtered = append(filtered, s)
			}
		}

		// Show our image suggestions only (no file completion) until the user indicates a path
		return filtered, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		noCache, _ := cmd.Flags().GetBool("no-cache")
		buildArgs, _ := cmd.Flags().GetStringArray("build-arg")
		removeExisting, _ := cmd.Flags().GetBool("rm-existing")
		overrideTag, _ := cmd.Flags().GetString("tag")
		squash, _ := cmd.Flags().GetBool("squash")

		pass := make([]string, 0, len(buildArgs)+1)
		if noCache {
			pass = append(pass, "--no-cache")
		}
		if squash {
			pass = append(pass, "--squash")
		}
		for _, kv := range buildArgs {
			pass = append(pass, "--build-arg", kv)
		}

		if removeExisting && docker.Exists(cfg.DefaultContainer) {
			fmt.Fprintf(cmd.OutOrStdout(), "Removing existing container %s...\n", cfg.DefaultContainer)
			_ = docker.Stop(cfg.DefaultContainer)
			_ = docker.Remove(cfg.DefaultContainer)
		}

		target := cfg.SelectedImage
		if len(args) == 1 && strings.TrimSpace(args[0]) != "" {
			target = args[0]
		}

		var dockerfilePath, contextDir string
		var imageTag string

		// Case 1: target is a path to a Dockerfile
		if fi, err := os.Stat(target); err == nil && !fi.IsDir() {
			dockerfilePath = target
			contextDir = filepath.Dir(target)
			// Use override tag if provided; else default to selected image's tag
			if overrideTag != "" {
				imageTag = overrideTag
			} else {
				sel := cfg.Images[cfg.SelectedImage]
				imageTag = sel.Tag
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Using local Dockerfile: %s\n", dockerfilePath)
		} else {
			// Case 2: target is a configured image name or stock keyword
			imgName := target
			img, ok := cfg.Images[imgName]
			if !ok {
				// Allow stock keywords without pre-adding
				if imgName == "discourse" {
					img = config.ImageConfig{Kind: "discourse", Tag: cfg.ImageTag, Workdir: cfg.Workdir, ContainerPort: cfg.ContainerPort, Dockerfile: config.ImageSource{Source: "stock", StockName: "discourse"}}
				} else if imgName == "theme" {
					img = config.ImageConfig{Kind: "theme", Tag: cfg.ThemeImageTag, Workdir: cfg.ThemeWorkdir, ContainerPort: cfg.ContainerPort, Dockerfile: config.ImageSource{Source: "stock", StockName: "theme"}}
				} else {
					return fmt.Errorf("unknown image '%s'", imgName)
				}
			}

			imageTag = img.Tag
			if overrideTag != "" {
				imageTag = overrideTag
			}

			var overridden bool
			var err2 error
			switch img.Dockerfile.Source {
			case "stock":
				switch img.Dockerfile.StockName {
				case "theme":
					dockerfilePath, contextDir, overridden, err2 = assets.ResolveDockerfileTheme(configDir)
					if err2 != nil {
						return err2
					}
					if overridden {
						fmt.Fprintf(cmd.OutOrStdout(), "Using override Dockerfile: %s\n", dockerfilePath)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "Using embedded theme Dockerfile (sha=%s) at: %s\n", assets.EmbeddedDockerfileThemeSHA256()[:12], dockerfilePath)
					}
				default:
					dockerfilePath, contextDir, overridden, err2 = assets.ResolveDockerfile(configDir)
					if err2 != nil {
						return err2
					}
					if overridden {
						fmt.Fprintf(cmd.OutOrStdout(), "Using override Dockerfile: %s\n", dockerfilePath)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "Using embedded Dockerfile (sha=%s) at: %s\n", assets.EmbeddedDockerfileSHA256()[:12], dockerfilePath)
					}
				}
			case "path":
				dockerfilePath = img.Dockerfile.Path
				contextDir = filepath.Dir(img.Dockerfile.Path)
				fmt.Fprintf(cmd.OutOrStdout(), "Using configured Dockerfile: %s\n", dockerfilePath)
			default:
				return fmt.Errorf("unsupported dockerfile source '%s'", img.Dockerfile.Source)
			}
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Building Docker image as: %s\n", imageTag)
		if err := docker.BuildFrom(imageTag, dockerfilePath, contextDir, pass); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Done.")
		return nil
	},
}

func init() {
	buildCmd.Flags().Bool("no-cache", false, "Do not use cache when building the image")
	buildCmd.Flags().StringArray("build-arg", nil, "Set build-time variables (KEY=VALUE)")
	buildCmd.Flags().Bool("rm-existing", false, "Remove existing default container before building")
	buildCmd.Flags().String("tag", "", "Override the Docker image tag for this build")
	buildCmd.Flags().Bool("squash", false, "Request Docker to squash layers into a single layer (requires experimental features)")
}
