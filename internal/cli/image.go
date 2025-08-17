package cli

import (
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strings"

    "github.com/spf13/cobra"

    "dv/internal/config"
    "dv/internal/xdg"
)

var imageCmd = &cobra.Command{
    Use:   "image",
    Short: "Manage dv images (list, select, add, remove, set, show)",
}

var imageListCmd = &cobra.Command{
    Use:   "list",
    Short: "List configured images and indicate the selected one",
    RunE: func(cmd *cobra.Command, args []string) error {
        configDir, err := xdg.ConfigDir()
        if err != nil { return err }
        cfg, err := config.LoadOrCreate(configDir)
        if err != nil { return err }
        names := make([]string, 0, len(cfg.Images))
        for n := range cfg.Images { names = append(names, n) }
        sort.Strings(names)
        for _, n := range names {
            mark := " "
            if n == cfg.SelectedImage { mark = "*" }
            img := cfg.Images[n]
            fmt.Fprintf(cmd.OutOrStdout(), "%s %-12s  tag=%s  kind=%s  workdir=%s  port=%d\n", mark, n, img.Tag, img.Kind, img.Workdir, img.ContainerPort)
        }
        return nil
    },
}

var imageSelectCmd = &cobra.Command{
    Use:   "select NAME",
    Short: "Select the default image",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        configDir, err := xdg.ConfigDir()
        if err != nil { return err }
        cfg, err := config.LoadOrCreate(configDir)
        if err != nil { return err }
        name := args[0]
        if _, ok := cfg.Images[name]; !ok {
            return fmt.Errorf("unknown image '%s'", name)
        }
        cfg.SelectedImage = name
        if err := config.Save(configDir, cfg); err != nil { return err }
        fmt.Fprintf(cmd.OutOrStdout(), "Selected image: %s\n", name)
        return nil
    },
}

var imageShowCmd = &cobra.Command{
    Use:   "show [NAME]",
    Short: "Show details for an image (default: selected)",
    Args:  cobra.RangeArgs(0, 1),
    RunE: func(cmd *cobra.Command, args []string) error {
        configDir, err := xdg.ConfigDir()
        if err != nil { return err }
        cfg, err := config.LoadOrCreate(configDir)
        if err != nil { return err }
        name := cfg.SelectedImage
        if len(args) == 1 && strings.TrimSpace(args[0]) != "" { name = args[0] }
        img, ok := cfg.Images[name]
        if !ok { return fmt.Errorf("unknown image '%s'", name) }
        fmt.Fprintf(cmd.OutOrStdout(), "name: %s\nkind: %s\ntag: %s\nworkdir: %s\ncontainerPort: %d\n", name, img.Kind, img.Tag, img.Workdir, img.ContainerPort)
        switch img.Dockerfile.Source {
        case "stock":
            fmt.Fprintf(cmd.OutOrStdout(), "dockerfile: stock(%s)\n", img.Dockerfile.StockName)
        case "path":
            fmt.Fprintf(cmd.OutOrStdout(), "dockerfile: %s\n", img.Dockerfile.Path)
        default:
            fmt.Fprintf(cmd.OutOrStdout(), "dockerfile: (unknown)\n")
        }
        return nil
    },
}

var imageAddCmd = &cobra.Command{
    Use:   "add NAME",
    Short: "Add a new image to config",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        configDir, err := xdg.ConfigDir()
        if err != nil { return err }
        cfg, err := config.LoadOrCreate(configDir)
        if err != nil { return err }

        name := args[0]
        if _, exists := cfg.Images[name]; exists {
            return fmt.Errorf("image '%s' already exists", name)
        }

        stock, _ := cmd.Flags().GetString("stock")
        dockerfilePath, _ := cmd.Flags().GetString("dockerfile")
        tag, _ := cmd.Flags().GetString("tag")
        workdir, _ := cmd.Flags().GetString("workdir")
        port, _ := cmd.Flags().GetInt("container-port")

        var src config.ImageSource
        var kind string
        switch {
        case stock != "":
            if stock != "discourse" && stock != "theme" {
                return fmt.Errorf("--stock must be 'discourse' or 'theme'")
            }
            src = config.ImageSource{Source: "stock", StockName: stock}
            kind = stock
            if tag == "" {
                if stock == "discourse" { tag = cfg.ImageTag } else { tag = cfg.ThemeImageTag }
            }
            if workdir == "" {
                if stock == "discourse" { workdir = cfg.Workdir } else { workdir = cfg.ThemeWorkdir }
            }
            if port == 0 { port = cfg.ContainerPort }
        case dockerfilePath != "":
            abs := dockerfilePath
            if !filepath.IsAbs(abs) {
                abs, _ = filepath.Abs(dockerfilePath)
            }
            if st, err := os.Stat(abs); err != nil || st.IsDir() {
                return fmt.Errorf("--dockerfile must point to a Dockerfile file")
            }
            src = config.ImageSource{Source: "path", Path: abs}
            kind = "custom"
            if tag == "" { return fmt.Errorf("--tag is required when using --dockerfile") }
            if workdir == "" { return fmt.Errorf("--workdir is required when using --dockerfile") }
            if port == 0 { return fmt.Errorf("--container-port is required when using --dockerfile") }
        default:
            return fmt.Errorf("specify one of --stock or --dockerfile")
        }

        if cfg.Images == nil { cfg.Images = map[string]config.ImageConfig{} }
        cfg.Images[name] = config.ImageConfig{
            Kind:          kind,
            Tag:           tag,
            Workdir:       workdir,
            ContainerPort: port,
            Dockerfile:    src,
        }
        if cfg.SelectedImage == "" { cfg.SelectedImage = name }
        if err := config.Save(configDir, cfg); err != nil { return err }
        fmt.Fprintf(cmd.OutOrStdout(), "Added image: %s\n", name)
        return nil
    },
}

var imageRemoveCmd = &cobra.Command{
    Use:   "remove NAME",
    Short: "Remove an image from config",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        configDir, err := xdg.ConfigDir()
        if err != nil { return err }
        cfg, err := config.LoadOrCreate(configDir)
        if err != nil { return err }
        name := args[0]
        if name == cfg.SelectedImage {
            return fmt.Errorf("cannot remove the selected image; select another first")
        }
        if _, ok := cfg.Images[name]; !ok {
            return fmt.Errorf("unknown image '%s'", name)
        }
        delete(cfg.Images, name)
        if err := config.Save(configDir, cfg); err != nil { return err }
        fmt.Fprintf(cmd.OutOrStdout(), "Removed image: %s\n", name)
        return nil
    },
}

var imageRenameCmd = &cobra.Command{
    Use:   "rename OLD NEW",
    Short: "Rename an image",
    Args:  cobra.ExactArgs(2),
    RunE: func(cmd *cobra.Command, args []string) error {
        configDir, err := xdg.ConfigDir()
        if err != nil { return err }
        cfg, err := config.LoadOrCreate(configDir)
        if err != nil { return err }
        oldName, newName := args[0], args[1]
        img, ok := cfg.Images[oldName]
        if !ok { return fmt.Errorf("unknown image '%s'", oldName) }
        if _, exists := cfg.Images[newName]; exists {
            return fmt.Errorf("image '%s' already exists", newName)
        }
        delete(cfg.Images, oldName)
        cfg.Images[newName] = img
        if cfg.SelectedImage == oldName { cfg.SelectedImage = newName }
        if cfg.ContainerImages != nil {
            for k, v := range cfg.ContainerImages {
                if v == oldName { cfg.ContainerImages[k] = newName }
            }
        }
        if err := config.Save(configDir, cfg); err != nil { return err }
        fmt.Fprintf(cmd.OutOrStdout(), "Renamed image '%s' -> '%s'\n", oldName, newName)
        return nil
    },
}

var imageSetCmd = &cobra.Command{
    Use:   "set NAME",
    Short: "Update properties of an image",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        configDir, err := xdg.ConfigDir()
        if err != nil { return err }
        cfg, err := config.LoadOrCreate(configDir)
        if err != nil { return err }
        name := args[0]
        img, ok := cfg.Images[name]
        if !ok { return fmt.Errorf("unknown image '%s'", name) }

        if v, _ := cmd.Flags().GetString("tag"); v != "" { img.Tag = v }
        if v, _ := cmd.Flags().GetString("workdir"); v != "" { img.Workdir = v }
        if v, _ := cmd.Flags().GetInt("container-port"); v != 0 { img.ContainerPort = v }
        if v, _ := cmd.Flags().GetString("stock"); v != "" {
            if v != "discourse" && v != "theme" { return fmt.Errorf("--stock must be discourse or theme") }
            img.Dockerfile = config.ImageSource{Source: "stock", StockName: v}
            if img.Kind == "custom" { img.Kind = v }
        }
        if v, _ := cmd.Flags().GetString("dockerfile"); v != "" {
            abs := v
            if !filepath.IsAbs(abs) { abs, _ = filepath.Abs(v) }
            if st, err := os.Stat(abs); err != nil || st.IsDir() { return fmt.Errorf("--dockerfile must point to a file") }
            img.Dockerfile = config.ImageSource{Source: "path", Path: abs}
            if img.Kind != "theme" && img.Kind != "discourse" { img.Kind = "custom" }
        }

        cfg.Images[name] = img
        if err := config.Save(configDir, cfg); err != nil { return err }
        fmt.Fprintf(cmd.OutOrStdout(), "Updated image: %s\n", name)
        return nil
    },
}

func init() {
    imageCmd.AddCommand(imageListCmd)
    imageCmd.AddCommand(imageSelectCmd)
    imageCmd.AddCommand(imageShowCmd)
    imageCmd.AddCommand(imageAddCmd)
    imageCmd.AddCommand(imageRemoveCmd)
    imageCmd.AddCommand(imageRenameCmd)
    imageCmd.AddCommand(imageSetCmd)

    imageAddCmd.Flags().String("stock", "", "Add a stock image: discourse|theme")
    imageAddCmd.Flags().String("dockerfile", "", "Path to a Dockerfile for a custom image")
    imageAddCmd.Flags().String("tag", "", "Docker image tag")
    imageAddCmd.Flags().String("workdir", "", "Working directory inside the container")
    imageAddCmd.Flags().Int("container-port", 0, "Container port to expose")

    imageSetCmd.Flags().String("tag", "", "Docker image tag")
    imageSetCmd.Flags().String("workdir", "", "Working directory inside the container")
    imageSetCmd.Flags().Int("container-port", 0, "Container port to expose")
    imageSetCmd.Flags().String("stock", "", "Switch dockerfile source to a stock image: discourse|theme")
    imageSetCmd.Flags().String("dockerfile", "", "Switch dockerfile source to a custom Dockerfile path")
}
