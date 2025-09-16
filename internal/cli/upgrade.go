package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dv/internal/xdg"
)

var (
	upgradeVersionFlag string
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade the dv binary to the latest release",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUpgrade(cmd)
	},
}

func setupUpgradeCommand() {
	upgradeCmd.Flags().StringVar(&upgradeVersionFlag, "version", "", "Install a specific version (e.g. v0.3.0)")
	rootCmd.AddCommand(upgradeCmd)
}

func runUpgrade(cmd *cobra.Command) error {
	ctx := cmd.Context()
	targetTag := strings.TrimSpace(upgradeVersionFlag)
	if targetTag == "" {
		info, err := fetchLatestRelease(ctx)
		if err != nil {
			return fmt.Errorf("fetch latest release: %w", err)
		}
		targetTag = info.TagName
	}
	if !strings.HasPrefix(targetTag, "v") {
		targetTag = "v" + targetTag
	}

	currentNorm := normalizeVersion(version)
	targetNorm := normalizeVersion(targetTag)
	if version != "dev" && currentNorm != "" && targetNorm != "" {
		cmp, ok := compareVersionStrings(currentNorm, targetNorm)
		if ok && cmp >= 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "dv is already up to date (%s).\n", version)
			if cmp > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "Requested version %s is not newer than the current version.\n", targetTag)
			}
			return nil
		}
	}

	assetVersion := targetNorm
	if assetVersion == "" {
		assetVersion = strings.TrimPrefix(targetTag, "v")
	}
	assetName := fmt.Sprintf("%s_%s_%s_%s.tar.gz", updateRepoName, assetVersion, runtime.GOOS, runtime.GOARCH)
	downloadURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", updateRepoOwner, updateRepoName, targetTag, assetName)

	tmpDir, err := os.MkdirTemp("", "dv-upgrade-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, assetName)
	if err := downloadFile(ctx, downloadURL, archivePath); err != nil {
		return err
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("determine executable path: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(exePath)
	if err == nil && resolvedPath != "" {
		exePath = resolvedPath
	}

	destDir := filepath.Dir(exePath)
	tempFile, err := os.CreateTemp(destDir, ".dv-upgrade-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("cannot create temp file in %s (permission denied). try running with sudo", destDir)
		}
		return err
	}
	tempPath := tempFile.Name()

	if err := extractBinaryTo(archivePath, tempFile); err != nil {
		tempFile.Close()
		os.Remove(tempPath)
		return err
	}

	if err := tempFile.Close(); err != nil {
		os.Remove(tempPath)
		return err
	}

	if err := os.Chmod(tempPath, 0o755); err != nil {
		os.Remove(tempPath)
		return err
	}

	if err := os.Rename(tempPath, exePath); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("failed to replace %s (permission denied). try running with sudo", exePath)
		}
		os.Remove(tempPath)
		return fmt.Errorf("replace binary: %w", err)
	}

	configDir, err := xdg.ConfigDir()
	if err == nil {
		if state, err := loadUpdateState(configDir); err == nil {
			state.LastChecked = time.Now().UTC()
			state.LatestVersion = targetTag
			state.NotifiedVersion = targetTag
			state.LastNotified = time.Now().UTC()
			state.LastError = ""
			_ = saveUpdateState(configDir, state)
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "dv upgraded to %s at %s\n", targetTag, exePath)
	return nil
}

func downloadFile(ctx context.Context, url string, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", updateUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("download failed (%d) for %s: %s", resp.StatusCode, url, strings.TrimSpace(string(body)))
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}
	return nil
}

func extractBinaryTo(archivePath string, dest *os.File) error {
	if _, err := dest.Seek(0, 0); err != nil {
		return err
	}
	if err := dest.Truncate(0); err != nil {
		return err
	}

	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()

	gz, err := gzip.NewReader(archive)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Base(header.Name)
		if name != updateRepoName && name != updateRepoName+".exe" {
			continue
		}
		if _, err := io.Copy(dest, tr); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("binary %s not found in archive", updateRepoName)
}
