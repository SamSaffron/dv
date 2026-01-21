package paste

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"dv/internal/docker"
)

// DockerExecConfig configures how to run docker exec with paste support.
type DockerExecConfig struct {
	ContainerName string
	Workdir       string
	Envs          docker.Envs
	Argv          []string
	User          string
	ImageTempDir  string // Directory in container for temp images (default: /tmp/dv-images)
}

// ExecWithPaste runs docker exec with paste interception enabled.
// Images pasted/referenced are automatically copied to the container.
func ExecWithPaste(cfg DockerExecConfig) error {
	if cfg.User == "" {
		cfg.User = "discourse"
	}
	if cfg.ImageTempDir == "" {
		cfg.ImageTempDir = "/tmp/dv-images"
	}

	// Ensure the temp directory exists in the container
	mkdirCmd := exec.Command("docker", "exec", "--user", cfg.User, cfg.ContainerName,
		"mkdir", "-p", cfg.ImageTempDir)
	mkdirCmd.Run() // Ignore errors, directory might already exist

	// Check if we have a TTY - if not, fall back to non-paste exec
	isTTY := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))

	// Build docker exec command
	args := []string{"exec", "-i"}
	if isTTY {
		args = append(args, "-t") // allocate pseudo-TTY
	}
	args = append(args, "--user", cfg.User, "-w", cfg.Workdir)
	for _, e := range cfg.Envs {
		args = append(args, "-e", e)
	}
	args = append(args, cfg.ContainerName)
	args = append(args, cfg.Argv...)

	cmd := exec.Command("docker", args...)

	if !isTTY {
		// No TTY, fall back to simple exec without paste interception
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Create image handler that copies images to container
	imageCounter := 0
	imageHandler := func(data []byte, format string) (string, error) {
		imageCounter++
		filename := fmt.Sprintf("pasted-%d-%d.%s", time.Now().Unix(), imageCounter, format)
		containerPath := filepath.Join(cfg.ImageTempDir, filename)

		// Write to temp file on host
		tmpFile, err := os.CreateTemp("", "dv-paste-*."+format)
		if err != nil {
			return "", err
		}
		defer os.Remove(tmpFile.Name())

		if _, err := tmpFile.Write(data); err != nil {
			tmpFile.Close()
			return "", err
		}
		tmpFile.Close()

		// Copy to container
		cpCmd := exec.Command("docker", "cp", tmpFile.Name(),
			fmt.Sprintf("%s:%s", cfg.ContainerName, containerPath))
		if err := cpCmd.Run(); err != nil {
			return "", err
		}

		// Set ownership
		chownCmd := exec.Command("docker", "exec", "--user", "root", cfg.ContainerName,
			"chown", cfg.User+":"+cfg.User, containerPath)
		chownCmd.Run() // Best effort

		return containerPath, nil
	}

	// Start PTY for the docker command
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("failed to start pty: %w", err)
	}

	// Handle terminal resize
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-done:
				return
			case <-sigCh:
				if ws, err := pty.GetsizeFull(os.Stdin); err == nil {
					pty.Setsize(ptmx, ws)
				}
			}
		}
	}()
	sigCh <- syscall.SIGWINCH // Initial resize

	// Put terminal in raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		ptmx.Close()
		close(done)
		return fmt.Errorf("failed to set raw mode: %w", err)
	}

	// Enable bracketed paste mode
	os.Stdout.Write([]byte("\x1b[?2004h"))

	// Create interceptor
	interceptor := NewInterceptor(imageHandler)

	// Copy PTY output to stdout
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if err != nil {
				return
			}
			os.Stdout.Write(buf[:n])
		}
	}()

	// Copy stdin to PTY with interception
	go func() {
		buf := make([]byte, 32*1024)
		for {
			select {
			case <-done:
				return
			default:
			}
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			processed := interceptor.Process(buf[:n])
			ptmx.Write(processed)
		}
	}()

	// Wait for command to finish
	err = cmd.Wait()

	// Cleanup
	close(done)
	signal.Stop(sigCh)
	os.Stdout.Write([]byte("\x1b[?2004l"))
	term.Restore(int(os.Stdin.Fd()), oldState)
	ptmx.Close()

	return err
}

// Interceptor processes input and detects/handles images.
type Interceptor struct {
	handler     ImageHandler
	pasteBuffer []byte
	inPaste     bool
	graphicsBuf []byte
	inGraphics  int    // 0=none, 1=kitty, 2=iterm2
	pending     []byte // buffer for potential partial escape sequences
}

// Maximum paste buffer size (10MB) to prevent unbounded growth
const maxPasteBuffer = 10 * 1024 * 1024

// NewInterceptor creates a new paste/graphics interceptor.
func NewInterceptor(handler ImageHandler) *Interceptor {
	return &Interceptor{handler: handler}
}

// Process handles input data, detecting and processing images.
func (i *Interceptor) Process(data []byte) []byte {
	// Prepend any pending data from previous call
	if len(i.pending) > 0 {
		data = append(i.pending, data...)
		i.pending = nil
	}

	var result []byte

	for j := 0; j < len(data); j++ {
		b := data[j]

		// State machine for paste detection
		if i.inPaste {
			i.pasteBuffer = append(i.pasteBuffer, b)

			// Safety: abort if paste buffer gets too large
			if len(i.pasteBuffer) > maxPasteBuffer {
				// Flush as-is, something went wrong
				result = append(result, []byte("\x1b[200~")...)
				result = append(result, i.pasteBuffer...)
				i.pasteBuffer = nil
				i.inPaste = false
				continue
			}

			// Check for end of bracketed paste
			if len(i.pasteBuffer) >= 6 {
				tail := i.pasteBuffer[len(i.pasteBuffer)-6:]
				if string(tail) == "\x1b[201~" {
					// End of paste - process content
					content := i.pasteBuffer[:len(i.pasteBuffer)-6]
					processed := i.processPasteContent(content)
					result = append(result, []byte("\x1b[200~")...)
					result = append(result, processed...)
					result = append(result, []byte("\x1b[201~")...)
					i.pasteBuffer = nil
					i.inPaste = false
				}
			}
			continue
		}

		// Handle graphics mode
		if i.inGraphics > 0 {
			i.graphicsBuf = append(i.graphicsBuf, b)

			// Safety: abort if graphics buffer gets too large
			if len(i.graphicsBuf) > maxPasteBuffer {
				result = append(result, i.graphicsBuf...)
				i.graphicsBuf = nil
				i.inGraphics = 0
				continue
			}

			// Check for end sequences
			ended := false
			if i.inGraphics == 1 { // Kitty: ESC \
				if len(i.graphicsBuf) >= 2 {
					tail := i.graphicsBuf[len(i.graphicsBuf)-2:]
					if tail[0] == '\x1b' && tail[1] == '\\' {
						ended = true
					}
				}
			} else if i.inGraphics == 2 { // iTerm2: BEL or ESC \
				if b == '\x07' {
					ended = true
				} else if len(i.graphicsBuf) >= 2 {
					tail := i.graphicsBuf[len(i.graphicsBuf)-2:]
					if tail[0] == '\x1b' && tail[1] == '\\' {
						ended = true
					}
				}
			}

			if ended {
				// Process graphics data
				if path := i.processGraphics(); path != "" {
					result = append(result, []byte(path)...)
				}
				i.inGraphics = 0
				i.graphicsBuf = nil
			}
			continue
		}

		// Check for escape sequences that might start special modes
		if b == '\x1b' {
			remaining := data[j:]

			// Check for bracketed paste start: ESC [ 2 0 0 ~
			if hasPrefix(remaining, "\x1b[200~") {
				i.inPaste = true
				i.pasteBuffer = nil
				j += 5 // Skip the rest of the sequence
				continue
			}

			// Check for Kitty keyboard protocol Ctrl+V Press: ESC [ 1 1 8 ; 5 u
			if hasPrefix(remaining, "\x1b[118;5u") {
				data, format, err := ReadClipboard()
				if err == nil {
					if format == "text" {
						processed := i.processFilePaths(data)
						result = append(result, processed...)
					} else {
						containerPath, err := i.handler(data, format)
						if err == nil {
							result = append(result, []byte(containerPath)...)
						}
					}
					j += 8 // Skip the entire sequence
					continue
				}
			}

			// Swallow Kitty keyboard protocol Ctrl+V Release: ESC [ 1 1 8 ; 5 : 3 u
			if hasPrefix(remaining, "\x1b[118;5:3u") {
				j += 10 // Skip and swallow the release sequence
				continue
			}

			// Check for Kitty graphics start: ESC _ G
			if hasPrefix(remaining, "\x1b_G") {
				i.inGraphics = 1
				i.graphicsBuf = []byte{'\x1b', '_', 'G'}
				j += 2
				continue
			}

			// Check for iTerm2 image start: ESC ] 1337 ; File=
			if hasPrefix(remaining, "\x1b]1337;File=") {
				i.inGraphics = 2
				i.graphicsBuf = []byte(remaining[:13])
				j += 12
				continue
			}

			// Check if this might be the start of a sequence split across reads
			if couldBePartialSequence(remaining) {
				i.pending = append([]byte{}, remaining...)
				break // Stop processing, wait for more data
			}
		}

		// Handle Ctrl+V (0x16) for magic paste if on Wayland/X11
		if b == '\x16' && i.handler != nil {
			data, format, err := ReadClipboard()
			if err == nil {
				if format == "text" {
					// Also process file paths in pasted text
					processed := i.processFilePaths(data)
					result = append(result, processed...)
				} else {
					// Handle as image
					containerPath, err := i.handler(data, format)
					if err == nil {
						result = append(result, []byte(containerPath)...)
					}
				}
				continue
			}
		}

		// Regular character
		result = append(result, b)
	}

	// If we're in paste/graphics mode, result may be partial
	if i.inPaste || i.inGraphics > 0 {
		return result
	}

	// Check for file paths in the final result
	return i.processFilePaths(result)
}

// hasPrefix checks if data starts with prefix
func hasPrefix(data []byte, prefix string) bool {
	return len(data) >= len(prefix) && string(data[:len(prefix)]) == prefix
}

// couldBePartialSequence checks if data could be the start of a special sequence
func couldBePartialSequence(data []byte) bool {
	// Known sequence prefixes we care about
	sequences := []string{
		"\x1b[200~",       // bracketed paste start
		"\x1b_G",          // Kitty graphics
		"\x1b]1337;File=", // iTerm2 image
		"\x1b[118;5u",     // Kitty Ctrl+V
		"\x1b[118;5:3u",   // Kitty Ctrl+V variation
	}

	for _, seq := range sequences {
		// Check if data is a prefix of the sequence
		if len(data) < len(seq) {
			if string(data) == seq[:len(data)] {
				return true
			}
		}
	}
	return false
}

// processPasteContent handles content within a bracketed paste.
func (i *Interceptor) processPasteContent(content []byte) []byte {
	// Check for file paths
	return i.processFilePaths(content)
}

// processFilePaths detects and handles image file paths.
func (i *Interceptor) processFilePaths(data []byte) []byte {
	if i.handler == nil {
		return data
	}

	text := string(data)

	// Collect all matches from both patterns
	type pathMatch struct {
		start, end int
		path       string
	}
	var allMatches []pathMatch

	// Find absolute paths
	for _, match := range imagePathPattern.FindAllStringSubmatchIndex(text, -1) {
		if len(match) >= 4 {
			allMatches = append(allMatches, pathMatch{match[2], match[3], text[match[2]:match[3]]})
		}
	}

	// Find home paths
	for _, match := range homePathPattern.FindAllStringSubmatchIndex(text, -1) {
		if len(match) >= 4 {
			allMatches = append(allMatches, pathMatch{match[2], match[3], text[match[2]:match[3]]})
		}
	}

	// Find data URIs
	for _, match := range dataURIPattern.FindAllStringSubmatchIndex(text, -1) {
		if len(match) >= 6 {
			allMatches = append(allMatches, pathMatch{match[0], match[1], text[match[0]:match[1]]})
		}
	}

	if len(allMatches) == 0 {
		return data
	}

	// Sort by start position descending (process in reverse order)
	for i := 0; i < len(allMatches)-1; i++ {
		for j := i + 1; j < len(allMatches); j++ {
			if allMatches[j].start > allMatches[i].start {
				allMatches[i], allMatches[j] = allMatches[j], allMatches[i]
			}
		}
	}

	result := text
	for _, m := range allMatches {
		var containerPath string

		// Check if it's a data URI
		if dataURIMatch := dataURIPattern.FindStringSubmatch(m.path); dataURIMatch != nil {
			format := dataURIMatch[1]
			imgData, err := decodeBase64([]byte(dataURIMatch[2]))
			if err == nil {
				containerPath, _ = i.handler(imgData, format)
			}
		} else {
			// It's a file path
			expanded := expandHomePath(m.path)

			// Check if file exists
			info, err := os.Stat(expanded)
			if err != nil || info.IsDir() || info.Size() > 50*1024*1024 {
				continue
			}

			imgData, err := os.ReadFile(expanded)
			if err != nil {
				continue
			}

			format := detectImageFormat(imgData)
			if format == "" {
				format = FormatFromExtension(expanded)
				if format == "" {
					continue
				}
			}

			containerPath, _ = i.handler(imgData, format)
		}

		if containerPath != "" {
			result = result[:m.start] + containerPath + result[m.end:]
		}
	}

	return []byte(result)
}

// processGraphics extracts image data from graphics protocol sequences.
func (i *Interceptor) processGraphics() string {
	if i.handler == nil || len(i.graphicsBuf) == 0 {
		return ""
	}

	var imgData []byte
	var format string

	if i.inGraphics == 1 {
		// Kitty: ESC _ G <params> ; <base64> ESC \
		// Find semicolon separator
		buf := i.graphicsBuf[3:] // Skip ESC _ G
		if len(buf) < 2 {
			return ""
		}
		buf = buf[:len(buf)-2] // Remove ESC \

		semicolon := -1
		for idx, b := range buf {
			if b == ';' {
				semicolon = idx
				break
			}
		}
		if semicolon == -1 {
			return ""
		}

		b64 := buf[semicolon+1:]
		var err error
		imgData, err = decodeBase64(b64)
		if err != nil {
			return ""
		}
		format = detectImageFormat(imgData)
		if format == "" {
			format = "png"
		}

	} else if i.inGraphics == 2 {
		// iTerm2: ESC ] 1337 ; File= <params> : <base64> BEL/ST
		// Find last colon before base64 data
		buf := i.graphicsBuf[13:] // Skip header

		// Remove terminator
		if buf[len(buf)-1] == '\x07' {
			buf = buf[:len(buf)-1]
		} else if len(buf) >= 2 && buf[len(buf)-2] == '\x1b' && buf[len(buf)-1] == '\\' {
			buf = buf[:len(buf)-2]
		}

		lastColon := -1
		for idx := len(buf) - 1; idx >= 0; idx-- {
			if buf[idx] == ':' {
				lastColon = idx
				break
			}
		}
		if lastColon == -1 {
			return ""
		}

		b64 := buf[lastColon+1:]
		var err error
		imgData, err = decodeBase64(b64)
		if err != nil {
			return ""
		}
		format = detectImageFormat(imgData)
		if format == "" {
			format = "png"
		}
	}

	if len(imgData) == 0 {
		return ""
	}

	containerPath, err := i.handler(imgData, format)
	if err != nil {
		return ""
	}
	return containerPath
}

// decodeBase64 handles standard, URL-safe, and raw (no padding) base64.
func decodeBase64(data []byte) ([]byte, error) {
	s := string(data)

	// Try standard encoding first (with padding)
	if result, err := base64.StdEncoding.DecodeString(s); err == nil {
		return result, nil
	}

	// Try raw standard encoding (no padding)
	if result, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return result, nil
	}

	// Try URL-safe encoding (with padding)
	if result, err := base64.URLEncoding.DecodeString(s); err == nil {
		return result, nil
	}

	// Try raw URL-safe encoding (no padding)
	return base64.RawURLEncoding.DecodeString(s)
}
