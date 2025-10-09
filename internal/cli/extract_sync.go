package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"dv/internal/docker"
)

type syncOptions struct {
	containerName    string
	containerWorkdir string
	localRepo        string
	logOut           io.Writer
	errOut           io.Writer
	debug            bool
}

type changeSource int

const (
	sourceHost changeSource = iota
	sourceContainer
)

type watcherEvent struct {
	source changeSource
	path   string
}

type changeKind int

const (
	changeModify changeKind = iota
	changeDelete
	changeRename
)

type trackedChange struct {
	kind    changeKind
	path    string
	oldPath string
}

type statusEntry struct {
	staged   rune
	unstaged rune
	path     string
	oldPath  string
}

type extractSync struct {
	ctx           context.Context
	cancel        context.CancelFunc
	containerName string
	workdir       string
	localRepo     string
	logOut        io.Writer
	errOut        io.Writer
	debug         bool
	events        chan watcherEvent
}

func runExtractSync(cmd *cobra.Command, opts syncOptions) error {
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	s := &extractSync{
		ctx:           ctx,
		cancel:        cancel,
		containerName: opts.containerName,
		workdir:       opts.containerWorkdir,
		localRepo:     opts.localRepo,
		logOut:        opts.logOut,
		errOut:        opts.errOut,
		debug:         opts.debug,
		events:        make(chan watcherEvent, 256),
	}
	defer cancel()

	if err := s.ensureInotify(); err != nil {
		return err
	}

	if err := s.run(); err != nil {
		return err
	}
	fmt.Fprintln(s.logOut, "✅ Sync stopped")
	return nil
}

func (s *extractSync) run() error {
	g, ctx := errgroup.WithContext(s.ctx)
	s.ctx = ctx

	g.Go(s.runHostWatcher)
	g.Go(s.runContainerWatcher)
	g.Go(s.processEvents)

	if err := g.Wait(); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	return nil
}

func (s *extractSync) runHostWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := s.addHostWatchers(watcher, s.localRepo); err != nil {
		return err
	}

	for {
		select {
		case <-s.ctx.Done():
			return nil
		case err := <-watcher.Errors:
			if err != nil {
				return err
			}
		case event := <-watcher.Events:
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			rel, ok := s.relativeFromLocal(event.Name)
			if !ok {
				continue
			}
			if rel == "" || rel == "." || shouldIgnoreRelative(rel) {
				continue
			}
			if event.Op&fsnotify.Create != 0 {
				// If a directory is created, watch it recursively
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					_ = s.addHostWatchers(watcher, event.Name)
					continue
				}
			}
			s.queueEvent(watcherEvent{source: sourceHost, path: rel})
		}
	}
}

func (s *extractSync) runContainerWatcher() error {
	args := []string{"exec", "--user", "discourse", "-w", s.workdir, s.containerName,
		"inotifywait", "-m", "-r",
		"-e", "modify", "-e", "create", "-e", "delete", "-e", "move",
		"--format", "%w%f|%e", "--exclude", "(^|/)\\.git(/|$)", "."}
	cmd := exec.CommandContext(s.ctx, "docker", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderrBuf strings.Builder
	cmd.Stderr = io.MultiWriter(s.errOut, &stderrBuf)
	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		select {
		case <-s.ctx.Done():
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
			return nil
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		absPath, ok := parseInotifyLine(line)
		if !ok {
			s.debugf("ignoring unrecognized inotify line: %s", line)
			continue
		}
		if !path.IsAbs(absPath) {
			absPath = path.Clean(path.Join(s.workdir, absPath))
		}
		rel, ok := s.relativeFromContainer(absPath)
		if !ok || rel == "" || rel == "." || shouldIgnoreRelative(rel) {
			s.debugf("ignoring container event outside workdir: abs=%s rel=%s", absPath, rel)
			continue
		}
		// Directory events do not need to be queued; file events will arrive as children are modified.
		s.debugf("queueing container event: abs=%s rel=%s", absPath, rel)
		s.queueEvent(watcherEvent{source: sourceContainer, path: rel})
	}

	if err := scanner.Err(); err != nil {
		if s.ctx.Err() != nil {
			return nil
		}
		msg := strings.TrimSpace(stderrBuf.String())
		if msg != "" {
			return fmt.Errorf("container watcher stream error: %w: %s", err, msg)
		}
		return fmt.Errorf("container watcher stream error: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		if s.ctx.Err() != nil {
			return nil
		}
		msg := strings.TrimSpace(stderrBuf.String())
		if msg != "" {
			return fmt.Errorf("container watcher exited: %w: %s", err, msg)
		}
		return fmt.Errorf("container watcher exited: %w", err)
	}
	return nil
}

func (s *extractSync) processEvents() error {
	const settleDelay = 250 * time.Millisecond
	hostPaths := make(map[string]struct{})
	containerPaths := make(map[string]struct{})
	timer := time.NewTimer(settleDelay)
	if !timer.Stop() {
		<-timer.C
	}
	timerActive := false

	flush := func() error {
		if len(hostPaths) > 0 {
			paths := mapKeys(hostPaths)
			if err := s.processHostChanges(paths); err != nil {
				return err
			}
			hostPaths = make(map[string]struct{})
		}
		if len(containerPaths) > 0 {
			paths := mapKeys(containerPaths)
			if err := s.processContainerChanges(paths); err != nil {
				return err
			}
			containerPaths = make(map[string]struct{})
		}
		return nil
	}

	for {
		select {
		case <-s.ctx.Done():
			if timerActive {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
			return flush()
		case event := <-s.events:
			if event.source == sourceHost {
				hostPaths[event.path] = struct{}{}
			} else {
				containerPaths[event.path] = struct{}{}
			}
			if timerActive {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
			timer.Reset(settleDelay)
			timerActive = true
		case <-timer.C:
			timerActive = false
			if err := flush(); err != nil {
				return err
			}
		}
	}
}

func (s *extractSync) processHostChanges(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	s.debugf("host events: %s", strings.Join(paths, ", "))
	entries, err := gitStatusPorcelainHost(s.localRepo, paths)
	if err != nil {
		return err
	}
	changes := buildTrackedChanges(entries)
	for _, change := range changes {
		if change.kind == changeRename && change.oldPath != "" {
			if shouldIgnoreRelative(change.oldPath) {
				continue
			}
			if err := s.removeInContainer(change.oldPath); err != nil {
				return err
			}
			fmt.Fprintf(s.logOut, "host → container: removed %s\n", change.oldPath)
		}
		switch change.kind {
		case changeDelete:
			if err := s.removeInContainer(change.path); err != nil {
				return err
			}
			fmt.Fprintf(s.logOut, "host → container: removed %s\n", change.path)
		case changeModify, changeRename:
			same, err := s.hashesMatch(change.path)
			if err != nil {
				return err
			}
			if same {
				s.debugf("host path %s already synchronized", change.path)
				continue
			}
			if err := s.copyHostToContainer(change.path); err != nil {
				return err
			}
			fmt.Fprintf(s.logOut, "host → container: updated %s\n", change.path)
		}
	}
	return nil
}

func (s *extractSync) processContainerChanges(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	s.debugf("container events: %s", strings.Join(paths, ", "))
	entries, err := gitStatusPorcelainContainer(s.containerName, s.workdir, paths)
	if err != nil {
		return err
	}
	changes := buildTrackedChanges(entries)
	for _, change := range changes {
		if change.kind == changeRename && change.oldPath != "" {
			if shouldIgnoreRelative(change.oldPath) {
				continue
			}
			if err := s.removeOnHost(change.oldPath); err != nil {
				return err
			}
			fmt.Fprintf(s.logOut, "container → host: removed %s\n", change.oldPath)
		}
		switch change.kind {
		case changeDelete:
			if err := s.removeOnHost(change.path); err != nil {
				return err
			}
			fmt.Fprintf(s.logOut, "container → host: removed %s\n", change.path)
		case changeModify, changeRename:
			same, err := s.hashesMatch(change.path)
			if err != nil {
				return err
			}
			if same {
				s.debugf("container path %s already synchronized", change.path)
				continue
			}
			if err := s.copyContainerToHost(change.path); err != nil {
				return err
			}
			fmt.Fprintf(s.logOut, "container → host: updated %s\n", change.path)
		}
	}
	return nil
}

func gitStatusPorcelainHost(repo string, paths []string) ([]statusEntry, error) {
	args := []string{"-c", "core.quotePath=false", "status", "--porcelain"}
	if len(paths) > 0 {
		args = append(args, "--")
		for _, p := range paths {
			args = append(args, filepath.FromSlash(p))
		}
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git status (host): %w: %s", err, strings.TrimSpace(string(out)))
	}
	return parseStatusOutput(string(out)), nil
}

func gitStatusPorcelainContainer(name, workdir string, paths []string) ([]statusEntry, error) {
	args := []string{"git", "-c", "core.quotePath=false", "status", "--porcelain"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := docker.ExecOutput(name, workdir, args)
	if err != nil {
		return nil, fmt.Errorf("git status (container): %w: %s", err, strings.TrimSpace(out))
	}
	return parseStatusOutput(out), nil
}

func parseStatusOutput(out string) []statusEntry {
	var entries []statusEntry
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) < 3 {
			continue
		}
		x := rune(line[0])
		y := rune(line[1])
		if x == '?' && y == '?' {
			continue
		}
		rest := strings.TrimSpace(line[3:])
		entry := statusEntry{staged: x, unstaged: y}
		if strings.Contains(rest, " -> ") {
			parts := strings.SplitN(rest, " -> ", 2)
			entry.oldPath = filepath.ToSlash(parts[0])
			entry.path = filepath.ToSlash(parts[1])
		} else {
			entry.path = filepath.ToSlash(rest)
		}
		entries = append(entries, entry)
	}
	return entries
}

func buildTrackedChanges(entries []statusEntry) []trackedChange {
	var out []trackedChange
	for _, e := range entries {
		if e.path == "" {
			continue
		}
		if shouldIgnoreRelative(e.path) {
			continue
		}
		if e.staged == 'R' || e.unstaged == 'R' {
			out = append(out, trackedChange{kind: changeRename, path: e.path, oldPath: e.oldPath})
			continue
		}
		if e.staged == 'D' || e.unstaged == 'D' {
			path := e.path
			if e.oldPath != "" {
				path = e.oldPath
			}
			out = append(out, trackedChange{kind: changeDelete, path: path})
			continue
		}
		out = append(out, trackedChange{kind: changeModify, path: e.path})
	}
	return out
}

func (s *extractSync) hashesMatch(rel string) (bool, error) {
	hostHash, err := s.hostHash(rel)
	if err != nil {
		return false, err
	}
	containerHash, err := s.containerHash(rel)
	if err != nil {
		return false, err
	}
	return hostHash != "" && hostHash == containerHash, nil
}

func (s *extractSync) hostHash(rel string) (string, error) {
	abs := filepath.Join(s.localRepo, filepath.FromSlash(rel))
	if _, err := os.Stat(abs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	cmd := exec.Command("git", "-C", s.localRepo, "hash-object", "--", filepath.FromSlash(rel))
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "does not exist") {
			return "", nil
		}
		return "", fmt.Errorf("git hash-object (host): %w: %s", err, msg)
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *extractSync) containerHash(rel string) (string, error) {
	args := []string{"git", "hash-object", "--", rel}
	out, err := docker.ExecOutput(s.containerName, s.workdir, args)
	if err != nil {
		msg := strings.TrimSpace(out)
		if strings.Contains(msg, "does not exist") {
			return "", nil
		}
		return "", fmt.Errorf("git hash-object (container): %w: %s", err, msg)
	}
	return strings.TrimSpace(out), nil
}

func (s *extractSync) copyHostToContainer(rel string) error {
	hostPath := filepath.Join(s.localRepo, filepath.FromSlash(rel))
	info, err := os.Stat(hostPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	destDir := path.Join(s.workdir, path.Dir(rel))
	if destDir == s.workdir || destDir == "." || destDir == "" {
		destDir = s.workdir
	}
	if err := s.ensureContainerDir(path.Dir(rel)); err != nil {
		return err
	}
	if err := docker.CopyToContainer(s.containerName, hostPath, destDir); err != nil {
		return err
	}
	// Ensure the discourse user retains ownership and write permissions
	mode := fmt.Sprintf("%04o", info.Mode().Perm())
	if _, err := docker.ExecAsRoot(s.containerName, s.workdir, []string{"chown", "discourse:discourse", rel}); err != nil {
		return fmt.Errorf("container chown %s: %w", rel, err)
	}
	if _, err := docker.ExecAsRoot(s.containerName, s.workdir, []string{"chmod", mode, rel}); err != nil {
		return fmt.Errorf("container chmod %s: %w", rel, err)
	}
	return nil
}

func (s *extractSync) copyContainerToHost(rel string) error {
	hostPath := filepath.Join(s.localRepo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return err
	}
	containerPath := path.Join(s.workdir, rel)
	return docker.CopyFromContainer(s.containerName, containerPath, hostPath)
}

func (s *extractSync) removeInContainer(rel string) error {
	cmd := []string{"bash", "-lc", "rm -rf -- " + shellQuote(rel)}
	if _, err := docker.ExecOutput(s.containerName, s.workdir, cmd); err != nil {
		return fmt.Errorf("container remove %s: %w", rel, err)
	}
	return nil
}

func (s *extractSync) removeOnHost(rel string) error {
	pathOnHost := filepath.Join(s.localRepo, filepath.FromSlash(rel))
	if _, err := os.Stat(pathOnHost); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.Remove(pathOnHost); err != nil {
		if err := os.RemoveAll(pathOnHost); err != nil {
			return err
		}
	}
	return nil
}

func (s *extractSync) ensureContainerDir(rel string) error {
	dir := rel
	if dir == "." || dir == "" {
		return nil
	}
	cmd := []string{"bash", "-lc", "mkdir -p " + shellQuote(rel)}
	if _, err := docker.ExecOutput(s.containerName, s.workdir, cmd); err != nil {
		return fmt.Errorf("container mkdir %s: %w", rel, err)
	}
	return nil
}

func (s *extractSync) addHostWatchers(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, ok := s.relativeFromLocal(path)
		if ok && shouldIgnoreRelative(rel) {
			return filepath.SkipDir
		}
		return w.Add(path)
	})
}

func (s *extractSync) relativeFromLocal(pathname string) (string, bool) {
	rel, err := filepath.Rel(s.localRepo, pathname)
	if err != nil {
		return "", false
	}
	if strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func (s *extractSync) relativeFromContainer(abs string) (string, bool) {
	if abs == "" {
		return "", false
	}
	clean := path.Clean(abs)
	work := path.Clean(s.workdir)
	if !strings.HasPrefix(clean, work) {
		return "", false
	}
	rel := strings.TrimPrefix(clean, work)
	rel = strings.TrimPrefix(rel, "/")
	return rel, true
}

func (s *extractSync) ensureInotify() error {
	out, err := docker.ExecOutput(s.containerName, s.workdir, []string{"bash", "-lc", "command -v inotifywait"})
	trimmed := strings.TrimSpace(out)
	if err != nil {
		if trimmed == "" {
			return fmt.Errorf("inotifywait not found in container; install inotify-tools (provides inotifywait)")
		}
		return fmt.Errorf("checking inotifywait: %w: %s", err, trimmed)
	}
	if trimmed == "" {
		return fmt.Errorf("inotifywait not found in container; install inotify-tools (provides inotifywait)")
	}
	return nil
}

func (s *extractSync) queueEvent(ev watcherEvent) {
	select {
	case <-s.ctx.Done():
		return
	case s.events <- ev:
	}
}

func (s *extractSync) debugf(format string, args ...interface{}) {
	if !s.debug {
		return
	}
	fmt.Fprintf(s.logOut, "[debug] "+format+"\n", args...)
}

func shouldIgnoreRelative(rel string) bool {
	if rel == "" {
		return false
	}
	clean := strings.TrimPrefix(rel, "./")
	clean = strings.TrimPrefix(clean, "/")
	return clean == ".git" || strings.HasPrefix(clean, ".git/") || strings.Contains(clean, "/.git/")
}

func mapKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func parseInotifyLine(line string) (string, bool) {
	if strings.Contains(line, "|") {
		parts := strings.SplitN(line, "|", 2)
		abs := strings.TrimSpace(parts[0])
		if abs == "" {
			return "", false
		}
		return path.Clean(abs), true
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", false
	}
	dir := fields[0]
	name := ""
	if len(fields) >= 3 {
		name = strings.Join(fields[2:], " ")
	}
	if name != "" {
		return path.Clean(path.Join(dir, name)), true
	}
	if dir == "" {
		return "", false
	}
	return path.Clean(dir), true
}
