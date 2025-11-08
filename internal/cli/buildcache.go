package cli

import (
	"path/filepath"
	"strings"

	"dv/internal/xdg"
)

func defaultBuildCacheDir(tag string) (string, error) {
	dataDir, err := xdg.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, "buildkit-cache", cacheKeyFromTag(tag)), nil
}

func cacheKeyFromTag(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range tag {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
