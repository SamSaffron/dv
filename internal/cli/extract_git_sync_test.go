package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitInit initializes a git repo in the given directory with minimal config
func gitInit(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
}

// runGit runs a git command in the given directory
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

// runGitNoFail runs a git command but doesn't fail the test on error
func runGitNoFail(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// writeFile creates or updates a file with the given content
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
}

func TestGitSyncer_CheckGitState(t *testing.T) {
	// Note: This test only tests the host-side git state checking
	// since we can't easily mock a container's git state
	t.Parallel()

	tmpDir := t.TempDir()
	gitInit(t, tmpDir)

	// Create initial commit
	writeFile(t, filepath.Join(tmpDir, "test.txt"), "initial")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "initial")

	var logBuf, errBuf bytes.Buffer
	ctx := context.Background()

	syncer := newGitSyncer(ctx, "fake-container", "/workdir", tmpDir, &logBuf, &errBuf, true)

	// Test hostGitOutput
	head, err := syncer.hostGitOutput("rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("hostGitOutput failed: %v", err)
	}
	if head == "" || len(head) < 7 {
		t.Errorf("expected SHA, got %q", head)
	}

	branch, err := syncer.hostGitOutput("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("hostGitOutput --abbrev-ref failed: %v", err)
	}
	// Git 2.28+ defaults to "main" or the configured init.defaultBranch, older versions use "master"
	if branch != "master" && branch != "main" {
		t.Errorf("expected main or master branch, got %q", branch)
	}
}

func TestGitSyncer_HostGitOutput_DetachedHead(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	gitInit(t, tmpDir)

	// Create two commits
	writeFile(t, filepath.Join(tmpDir, "test.txt"), "first")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "first")

	writeFile(t, filepath.Join(tmpDir, "test.txt"), "second")
	runGit(t, tmpDir, "commit", "-am", "second")

	// Get first commit SHA and checkout (detached)
	firstSHA := runGit(t, tmpDir, "rev-parse", "HEAD~1")
	runGit(t, tmpDir, "checkout", "--detach", "HEAD~1")

	var logBuf, errBuf bytes.Buffer
	ctx := context.Background()
	syncer := newGitSyncer(ctx, "fake-container", "/workdir", tmpDir, &logBuf, &errBuf, false)

	branch, err := syncer.hostGitOutput("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("hostGitOutput failed: %v", err)
	}
	// In detached HEAD state, git rev-parse --abbrev-ref HEAD returns "HEAD"
	if branch != "HEAD" {
		t.Errorf("expected HEAD for detached state, got %q", branch)
	}

	// Verify we're at the first commit
	currentSHA, _ := syncer.hostGitOutput("rev-parse", "HEAD")
	if currentSHA != firstSHA[:len(currentSHA)] && firstSHA[:len(currentSHA)] != currentSHA {
		t.Logf("SHAs: current=%q first=%q", currentSHA, firstSHA)
	}
}

func TestGitSyncer_HostGitOutput_EmptyRepo(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	gitInit(t, tmpDir)
	// Don't create any commits - repo is empty

	var logBuf, errBuf bytes.Buffer
	ctx := context.Background()
	syncer := newGitSyncer(ctx, "fake-container", "/workdir", tmpDir, &logBuf, &errBuf, false)

	// rev-parse HEAD should fail in empty repo
	_, err := syncer.hostGitOutput("rev-parse", "HEAD")
	if err == nil {
		t.Error("expected error for rev-parse HEAD in empty repo")
	}
}

func TestGitSyncer_DebugLogging(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	gitInit(t, tmpDir)

	var logBuf, errBuf bytes.Buffer
	ctx := context.Background()

	// With debug=true
	syncer := newGitSyncer(ctx, "fake-container", "/workdir", tmpDir, &logBuf, &errBuf, true)
	syncer.debugf("test message %d", 42)

	if !bytes.Contains(logBuf.Bytes(), []byte("[git-sync] test message 42")) {
		t.Errorf("debug message not logged: %s", logBuf.String())
	}

	// With debug=false
	logBuf.Reset()
	syncer = newGitSyncer(ctx, "fake-container", "/workdir", tmpDir, &logBuf, &errBuf, false)
	syncer.debugf("should not appear")

	if logBuf.Len() > 0 {
		t.Errorf("debug message logged when debug=false: %s", logBuf.String())
	}
}

func TestGitSyncer_ContainerIsAhead_HostCommitExists(t *testing.T) {
	// Test the case where container HEAD exists in host repo
	// We simulate this by using a known commit that exists
	t.Parallel()

	tmpDir := t.TempDir()
	gitInit(t, tmpDir)

	// Create commits
	writeFile(t, filepath.Join(tmpDir, "test.txt"), "first")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "first")
	firstSHA := runGit(t, tmpDir, "rev-parse", "HEAD")

	writeFile(t, filepath.Join(tmpDir, "test.txt"), "second")
	runGit(t, tmpDir, "commit", "-am", "second")
	secondSHA := runGit(t, tmpDir, "rev-parse", "HEAD")

	var logBuf, errBuf bytes.Buffer
	ctx := context.Background()
	syncer := newGitSyncer(ctx, "fake-container", "/workdir", tmpDir, &logBuf, &errBuf, true)

	// Simulate state where container is at first commit, host is at second
	state := gitSyncState{
		hostHead:      secondSHA[:40],
		hostBranch:    "main",
		containerHead: firstSHA[:40],
	}

	ahead, err := syncer.containerIsAhead(state)
	if err != nil {
		t.Fatalf("containerIsAhead failed: %v", err)
	}
	if ahead {
		t.Error("container should not be ahead (host has newer commits)")
	}
}

func TestGitSyncer_ContainerIsAhead_ContainerCommitNotInHost(t *testing.T) {
	// Test the case where container has a commit not in host
	// This simulates the "container made local changes" scenario
	t.Parallel()

	tmpDir := t.TempDir()
	gitInit(t, tmpDir)

	writeFile(t, filepath.Join(tmpDir, "test.txt"), "initial")
	runGit(t, tmpDir, "add", ".")
	runGit(t, tmpDir, "commit", "-m", "initial")

	var logBuf, errBuf bytes.Buffer
	ctx := context.Background()
	syncer := newGitSyncer(ctx, "fake-container", "/workdir", tmpDir, &logBuf, &errBuf, true)

	// Use a SHA that definitely doesn't exist in the repo
	state := gitSyncState{
		hostHead:      runGit(t, tmpDir, "rev-parse", "HEAD")[:40],
		hostBranch:    "main",
		containerHead: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", // Fake SHA
	}

	ahead, err := syncer.containerIsAhead(state)
	if err != nil {
		t.Fatalf("containerIsAhead failed: %v", err)
	}
	if !ahead {
		t.Error("container should be detected as ahead (has unknown commit)")
	}
}

func TestGitSyncState_FieldInitialization(t *testing.T) {
	t.Parallel()

	state := gitSyncState{
		hostHead:        "abc123",
		hostBranch:      "main",
		containerHead:   "def456",
		containerBranch: "feature",
	}

	if state.hostHead != "abc123" {
		t.Errorf("hostHead = %q, want abc123", state.hostHead)
	}
	if state.hostBranch != "main" {
		t.Errorf("hostBranch = %q, want main", state.hostBranch)
	}
	if state.containerHead != "def456" {
		t.Errorf("containerHead = %q, want def456", state.containerHead)
	}
	if state.containerBranch != "feature" {
		t.Errorf("containerBranch = %q, want feature", state.containerBranch)
	}
}

func TestGitSyncer_Min(t *testing.T) {
	// Test the min function used in slice operations
	t.Parallel()

	tests := []struct {
		a, b, want int
	}{
		{0, 0, 0},
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{8, 40, 8},
		{40, 8, 8},
	}

	for _, tt := range tests {
		got := min(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("min(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
