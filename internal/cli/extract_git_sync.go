package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"dv/internal/docker"
)

// gitSyncState tracks the last known git state for change detection
type gitSyncState struct {
	hostHead        string // SHA of host HEAD
	hostBranch      string // Current branch name (or "HEAD" if detached)
	containerHead   string // SHA of container HEAD
	containerBranch string // Container's current branch
}

// gitSyncer handles git state synchronization between host and container
type gitSyncer struct {
	ctx           context.Context
	containerName string
	workdir       string
	localRepo     string
	logOut        io.Writer
	errOut        io.Writer
	debug         bool
}

// newGitSyncer creates a new git synchronizer
func newGitSyncer(ctx context.Context, containerName, workdir, localRepo string, logOut, errOut io.Writer, debug bool) *gitSyncer {
	return &gitSyncer{
		ctx:           ctx,
		containerName: containerName,
		workdir:       workdir,
		localRepo:     localRepo,
		logOut:        logOut,
		errOut:        errOut,
		debug:         debug,
	}
}

// checkGitState reads current git state from both host and container
func (g *gitSyncer) checkGitState() (gitSyncState, error) {
	var state gitSyncState
	var err error

	// Host state
	state.hostHead, err = g.hostGitOutput("rev-parse", "HEAD")
	if err != nil {
		return state, fmt.Errorf("host git rev-parse HEAD: %w", err)
	}

	state.hostBranch, _ = g.hostGitOutput("rev-parse", "--abbrev-ref", "HEAD")
	if state.hostBranch == "" {
		state.hostBranch = "HEAD" // Detached
	}

	// Container state
	state.containerHead, err = g.containerGitOutput("rev-parse", "HEAD")
	if err != nil {
		return state, fmt.Errorf("container git rev-parse HEAD: %w", err)
	}

	state.containerBranch, _ = g.containerGitOutput("rev-parse", "--abbrev-ref", "HEAD")
	if state.containerBranch == "" {
		state.containerBranch = "HEAD"
	}

	return state, nil
}

// syncToContainer synchronizes git state from host to container
func (g *gitSyncer) syncToContainer() error {
	state, err := g.checkGitState()
	if err != nil {
		return err
	}

	// Already in sync (exact SHA match)
	if state.hostHead == state.containerHead && state.hostBranch == state.containerBranch {
		g.debugf("git already in sync at %s (%s)", state.hostHead[:min(8, len(state.hostHead))], state.hostBranch)
		return nil
	}

	g.debugf("syncing git: host=%s (%s) container=%s (%s)",
		state.hostHead[:min(8, len(state.hostHead))], state.hostBranch,
		state.containerHead[:min(8, len(state.containerHead))], state.containerBranch)

	// Check if container is ahead of host (has commits host doesn't have)
	ahead, err := g.containerIsAhead(state)
	if err != nil {
		return fmt.Errorf("checking if container ahead: %w", err)
	}
	if ahead {
		fmt.Fprintf(g.errOut, "warning: container has commits not in host, skipping git sync\n")
		return nil
	}

	// Create bundle with missing commits
	bundlePath, cleanup, err := g.createBundle(state)
	if err != nil {
		return fmt.Errorf("creating bundle: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Fetch bundle into container if there are commits to transfer
	if bundlePath != "" {
		if err := g.applyBundle(bundlePath, state); err != nil {
			return fmt.Errorf("applying bundle: %w", err)
		}
	}

	// Checkout to the exact commit/branch
	if err := g.syncBranch(state); err != nil {
		return fmt.Errorf("syncing branch: %w", err)
	}

	fmt.Fprintf(g.logOut, "git sync: container now at %s (%s)\n",
		state.hostHead[:min(8, len(state.hostHead))], state.hostBranch)

	return nil
}

// containerIsAhead checks if container has commits not in host
func (g *gitSyncer) containerIsAhead(state gitSyncState) (bool, error) {
	// Check if container's HEAD commit exists in host
	cmd := exec.CommandContext(g.ctx, "git", "cat-file", "-e", state.containerHead+"^{commit}")
	cmd.Dir = g.localRepo
	if err := cmd.Run(); err != nil {
		// Container has a commit not in host
		g.debugf("container HEAD %s not found in host", state.containerHead[:min(8, len(state.containerHead))])
		return true, nil
	}

	// Container commit exists in host, check if ahead by commit count
	out, err := g.hostGitOutput("rev-list", "--count", state.hostHead+".."+state.containerHead)
	if err != nil {
		return false, err
	}
	count := strings.TrimSpace(out)
	ahead := count != "0" && count != ""
	if ahead {
		g.debugf("container is %s commits ahead of host", count)
	}
	return ahead, nil
}

// createBundle creates a git bundle containing commits missing in container
// Returns the bundle path, cleanup function, and whether bundle has commits.
// If no commits need to be transferred, returns empty path with no error.
func (g *gitSyncer) createBundle(state gitSyncState) (string, func(), error) {
	// Check if container HEAD exists in host (needed as base for bundle)
	cmd := exec.CommandContext(g.ctx, "git", "cat-file", "-e", state.containerHead+"^{commit}")
	cmd.Dir = g.localRepo
	if cmd.Run() != nil {
		g.debugf("container HEAD %s not found in host", state.containerHead[:min(8, len(state.containerHead))])
		// Container has commits not in host - can't create bundle
		return "", nil, nil
	}

	// Check if there are any commits to bundle
	out, err := g.hostGitOutput("rev-list", "--count", state.containerHead+".."+state.hostBranch)
	if err != nil {
		return "", nil, fmt.Errorf("counting commits: %w", err)
	}

	count := strings.TrimSpace(out)
	if count == "0" || count == "" {
		// No new commits to transfer, just need branch sync
		g.debugf("no new commits to bundle")
		return "", nil, nil
	}

	// Create temp file for bundle
	tmpFile, err := os.CreateTemp("", "dv-gitsync-*.bundle")
	if err != nil {
		return "", nil, err
	}
	bundlePath := tmpFile.Name()
	tmpFile.Close()

	cleanup := func() { os.Remove(bundlePath) }

	// Create bundle with commits from container HEAD to host HEAD
	// Include the branch ref so container can fetch it
	bundleCmd := exec.CommandContext(g.ctx, "git", "bundle", "create", bundlePath,
		"^"+state.containerHead, state.hostBranch)
	bundleCmd.Dir = g.localRepo
	if out, err := bundleCmd.CombinedOutput(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git bundle create: %w: %s", err, strings.TrimSpace(string(out)))
	}

	g.debugf("created bundle with %s commit(s)", count)
	return bundlePath, cleanup, nil
}

// applyBundle fetches commits from a bundle file into the container
// This preserves exact commit SHAs unlike git am
func (g *gitSyncer) applyBundle(bundlePath string, state gitSyncState) error {
	// Reset container working tree to HEAD before fetching.
	// File sync may have already synced the changed files.
	if _, err := g.containerGitOutput("reset", "--hard", "HEAD"); err != nil {
		g.debugf("git reset --hard failed: %v", err)
	}

	// Copy bundle to container
	containerBundle := "/tmp/dv-gitsync.bundle"
	if err := docker.CopyToContainerWithOwnership(g.containerName, bundlePath, containerBundle, false); err != nil {
		return fmt.Errorf("copying bundle to container: %w", err)
	}

	// Fetch from bundle - this brings in the exact commits with same SHAs
	out, err := g.containerGitOutput("fetch", containerBundle, state.hostBranch)
	if err != nil {
		return fmt.Errorf("git fetch from bundle: %s", out)
	}

	// Clean up bundle in container
	docker.ExecOutput(g.containerName, "/", []string{"rm", "-f", containerBundle})

	return nil
}

// syncBranch updates the container's branch to match host's exact commit
func (g *gitSyncer) syncBranch(state gitSyncState) error {
	// With bundles, we have exact commit SHAs. Just checkout to the right place.
	if state.hostBranch == "HEAD" {
		// Host is detached
		out, err := g.containerGitOutput("checkout", "--detach", state.hostHead)
		if err != nil {
			return fmt.Errorf("git checkout --detach %s: %s", state.hostHead[:min(8, len(state.hostHead))], out)
		}
		return nil
	}

	// Checkout/create branch at exact commit
	out, err := g.containerGitOutput("checkout", "-B", state.hostBranch, state.hostHead)
	if err != nil {
		return fmt.Errorf("git checkout -B %s %s: %s", state.hostBranch, state.hostHead[:min(8, len(state.hostHead))], out)
	}
	return nil
}

// hostGitOutput runs a git command on the host and returns output
func (g *gitSyncer) hostGitOutput(args ...string) (string, error) {
	cmd := exec.CommandContext(g.ctx, "git", args...)
	cmd.Dir = g.localRepo
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// containerGitOutput runs a git command in the container and returns output
func (g *gitSyncer) containerGitOutput(args ...string) (string, error) {
	fullArgs := append([]string{"git"}, args...)
	out, err := docker.ExecOutput(g.containerName, g.workdir, fullArgs)
	return strings.TrimSpace(out), err
}

func (g *gitSyncer) debugf(format string, args ...interface{}) {
	if !g.debug {
		return
	}
	fmt.Fprintf(g.logOut, "[git-sync] "+format+"\n", args...)
}
