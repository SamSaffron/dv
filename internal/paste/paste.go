// Package paste provides PTY-based paste interception with image detection.
// It supports:
// - Bracketed paste mode detection (standard terminal feature)
// - Terminal graphics protocols (Kitty, iTerm2)
// - File path detection for local images
package paste

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ImageHandler is called when an image is detected in pasted content.
// It receives the image data and returns a path that should be substituted.
type ImageHandler func(data []byte, format string) (containerPath string, err error)

// Common patterns for image detection
var (
	// File path patterns for images
	imagePathPattern = regexp.MustCompile(`(?:^|[\s'"])(/[^\s'"]+\.(?:png|jpg|jpeg|gif|webp|bmp|tiff?|heic|avif))(?:[\s'"]|$)`)
	homePathPattern  = regexp.MustCompile(`(?:^|[\s'"])(~/[^\s'"]+\.(?:png|jpg|jpeg|gif|webp|bmp|tiff?|heic|avif))(?:[\s'"]|$)`)

	// Base64 image data URI pattern
	dataURIPattern = regexp.MustCompile(`data:image/(png|jpeg|gif|webp);base64,([A-Za-z0-9+/=]+)`)
)

// detectImageFormat returns the image format based on magic bytes.
func detectImageFormat(data []byte) string {
	if len(data) < 8 {
		return ""
	}

	// PNG: 89 50 4E 47 0D 0A 1A 0A
	if bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
		return "png"
	}

	// JPEG: FF D8 FF
	if bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}) {
		return "jpeg"
	}

	// GIF: GIF87a or GIF89a
	if bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")) {
		return "gif"
	}

	// WebP: RIFF....WEBP
	if len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return "webp"
	}

	// BMP: BM
	if bytes.HasPrefix(data, []byte("BM")) {
		return "bmp"
	}

	// TIFF: II (little-endian) or MM (big-endian)
	if bytes.HasPrefix(data, []byte{0x49, 0x49, 0x2A, 0x00}) || bytes.HasPrefix(data, []byte{0x4D, 0x4D, 0x00, 0x2A}) {
		return "tiff"
	}

	// HEIC/HEIF: ftyp followed by heic, heix, hevc, hevx, mif1, msf1
	if len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) {
		brand := string(data[8:12])
		if strings.HasPrefix(brand, "heic") || strings.HasPrefix(brand, "heix") ||
			strings.HasPrefix(brand, "hevc") || strings.HasPrefix(brand, "hevx") ||
			brand == "mif1" || brand == "msf1" {
			return "heic"
		}
	}

	// AVIF: ftyp followed by avif
	if len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) {
		if string(data[8:12]) == "avif" {
			return "avif"
		}
	}

	return ""
}

// expandHomePath expands ~/... paths to absolute paths.
func expandHomePath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// FormatFromExtension returns the image format based on file extension.
func FormatFromExtension(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "png"
	case ".jpg", ".jpeg":
		return "jpeg"
	case ".gif":
		return "gif"
	case ".webp":
		return "webp"
	case ".bmp":
		return "bmp"
	case ".tiff", ".tif":
		return "tiff"
	case ".heic", ".heif":
		return "heic"
	case ".avif":
		return "avif"
	default:
		return ""
	}
}
