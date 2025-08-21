package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"dv/internal/config"
	"dv/internal/xdg"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List containers created from the selected image",
	RunE: func(cmd *cobra.Command, args []string) error {
		configDir, err := xdg.ConfigDir()
		if err != nil {
			return err
		}
		cfg, err := config.LoadOrCreate(configDir)
		if err != nil {
			return err
		}

		imgName, imgCfg, err := resolveImage(cfg, "")
		if err != nil {
			return err
		}

		// Include Ports and Labels columns for discovery and clickable URLs
		out, _ := runShell("docker ps -a --format '{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}\t{{.Labels}}'")
		selected := cfg.SelectedAgent
		printed := false
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 5)
			if len(parts) < 3 {
				continue
			}
			name, image, status := parts[0], parts[1], parts[2]
			portsField := ""
			if len(parts) >= 4 {
				portsField = parts[3]
			}
			labelsField := ""
			if len(parts) >= 5 {
				labelsField = parts[4]
			}
			// Determine if this container belongs to the selected image
			belongs := false
			if imgNameFromCfg, ok := cfg.ContainerImages[name]; ok && imgNameFromCfg == imgName {
				belongs = true
			}
			if !belongs {
				if labelMap := parseLabels(labelsField); labelMap["com.dv.owner"] == "dv" && labelMap["com.dv.image-name"] == imgName {
					belongs = true
				}
			}
			if !belongs {
				// Legacy fallback: match by raw image tag
				if image == imgCfg.Tag {
					belongs = true
				}
			}
			if !belongs {
				continue
			}
			mark := " "
			if selected != "" && name == selected {
				mark = "*"
			}
			// Derive clickable localhost URLs from published host ports
			urls := parseHostPortURLs(portsField)
			if len(urls) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\t%s\t%s\n", mark, name, status, strings.Join(urls, " "))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\t%s\n", mark, name, status)
			}
			printed = true
		}
		if !printed {
			fmt.Fprintf(cmd.OutOrStdout(), "(no agents found for image '%s')\n", imgCfg.Tag)
		}
		if selected != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Selected: %s\n", selected)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Selected: (none)")
		}
		_ = imgName // not printed but kept for clarity
		return nil
	},
}

// parseHostPortURLs extracts host ports from a Docker "Ports" column value and
// returns clickable http://localhost:<port> URLs.
// Examples of input formats handled:
//
//	"0.0.0.0:4201->4200/tcp, :::4201->4200/tcp"
//	"127.0.0.1:8080->8080/tcp"
//	"4200/tcp" (no published ports)
func parseHostPortURLs(portsField string) []string {
	portsField = strings.TrimSpace(portsField)
	if portsField == "" {
		return nil
	}
	// Multiple mappings separated by commas
	segments := strings.Split(portsField, ",")
	var urls []string
	seen := map[string]struct{}{}
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		// Look for the left side before "->" which contains host ip:port
		arrowIdx := strings.Index(seg, "->")
		if arrowIdx == -1 {
			// Not a published mapping (e.g., "4200/tcp")
			continue
		}
		left := strings.TrimSpace(seg[:arrowIdx])
		// left may be like "0.0.0.0:4201" or ":::4201" or "127.0.0.1:4201"
		colonIdx := strings.LastIndex(left, ":")
		if colonIdx == -1 || colonIdx+1 >= len(left) {
			continue
		}
		hostPort := left[colonIdx+1:]
		// Basic numeric validation
		if hostPort == "" {
			continue
		}
		url := "http://localhost:" + hostPort
		if _, ok := seen[url]; !ok {
			seen[url] = struct{}{}
			urls = append(urls, url)
		}
	}
	return urls
}

// parseLabels converts a docker --format {{.Labels}} string (comma-separated key=value pairs)
// into a map for easy lookup. Malformed entries are ignored.
func parseLabels(labelsField string) map[string]string {
	labelsField = strings.TrimSpace(labelsField)
	if labelsField == "" {
		return map[string]string{}
	}
	items := strings.Split(labelsField, ",")
	out := make(map[string]string, len(items))
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		kv := strings.SplitN(it, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		if key != "" {
			out[key] = val
		}
	}
	return out
}
