package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/docker"
	"dv/internal/xdg"
)

const defaultPullRepository = "samsaffron/discourse-dv"

var pullCmd = &cobra.Command{
	Use:   "pull [IMAGE]",
	Short: "Pull the published dv image and tag it locally",
	Long: `Pull the published dv image from Docker Hub (default: samsaffron/discourse-dv)
and retag it to match the configured local image name. When IMAGE is provided,
that configured image is targeted; otherwise the currently selected image is used.`,
	Args: cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		remoteTag, _ := cmd.Flags().GetString("remote-tag")

		repo = strings.TrimSpace(repo)
		remoteTag = strings.TrimSpace(remoteTag)
		if repo == "" {
			repo = defaultPullRepository
		}

		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		override := ""
		if len(args) == 1 && strings.TrimSpace(args[0]) != "" {
			override = args[0]
		}
		imageName, imageCfg, err := resolveImage(cfg, override)
		if err != nil {
			return err
		}

		sourceRef := repo
		if !strings.ContainsAny(repo, ":@") {
			if remoteTag == "" {
				remoteTag = "latest"
			}
			sourceRef = fmt.Sprintf("%s:%s", repo, remoteTag)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Pulling image %s...\n", sourceRef)
		if err := docker.Pull(sourceRef); err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Tagging %s as %s...\n", sourceRef, imageCfg.Tag)
		if err := docker.TagImage(sourceRef, imageCfg.Tag); err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Image '%s' is now available as '%s'.\n", imageName, imageCfg.Tag)
		return nil
	},
}

func init() {
	pullCmd.Flags().String("repo", defaultPullRepository, "Remote repository to pull from (default samsaffron/discourse-dv)")
	pullCmd.Flags().String("remote-tag", "latest", "Remote tag to pull when repo does not include one")
}
