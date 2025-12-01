package docker

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/term"
)

// BuildOptions controls how docker images are built.
type BuildOptions struct {
	ExtraArgs    []string // additional docker build args supplied by callers
	ForceClassic bool     // skip buildx/BuildKit helpers and use legacy docker build
	Builder      string   // optional buildx builder name
}

func Exists(name string) bool {
	out, _ := exec.Command("bash", "-lc", "docker ps -aq -f name=^"+shellEscape(name)+"$").Output()
	return strings.TrimSpace(string(out)) != ""
}

func Running(name string) bool {
	out, _ := exec.Command("bash", "-lc", "docker ps -q -f status=running -f name=^"+shellEscape(name)+"$").Output()
	return strings.TrimSpace(string(out)) != ""
}

func Stop(name string) error {
	cmd := exec.Command("docker", "stop", name)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func Remove(name string) error {
	cmd := exec.Command("docker", "rm", name)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func Rename(oldName, newName string) error {
	cmd := exec.Command("docker", "rename", oldName, newName)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func Pull(ref string) error {
	cmd := exec.Command("docker", "pull", ref)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func Build(tag string, args []string) error {
	argv := []string{"build", "-t", tag}
	argv = append(argv, args...)
	argv = append(argv, ".")
	cmd := exec.Command("docker", argv...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// BuildFrom builds a Docker image from a specific Dockerfile and context
// directory. dockerfilePath may be absolute or relative; contextDir must be
// a directory.
func BuildFrom(tag, dockerfilePath, contextDir string, opts BuildOptions) error {
	if !filepath.IsAbs(dockerfilePath) {
		// ensure relative dockerfile path is evaluated relative to contextDir
		dockerfilePath = filepath.Join(contextDir, dockerfilePath)
	}
	if opts.ExtraArgs == nil {
		opts.ExtraArgs = []string{}
	}
	if opts.Builder == "" {
		if env := strings.TrimSpace(os.Getenv("DV_BUILDX_BUILDER")); env != "" {
			opts.Builder = env
		} else if env := strings.TrimSpace(os.Getenv("DV_BUILDER")); env != "" {
			opts.Builder = env
		}
	}
	useClassic := opts.ForceClassic || isTruthyEnv("DV_DISABLE_BUILDX")
	buildxOK := buildxAvailable()
	if !useClassic && buildxOK {
		return runBuildx(tag, dockerfilePath, contextDir, opts)
	}
	if !opts.ForceClassic && !buildxOK {
		if err := buildxError(); err != nil {
			fmt.Fprintf(os.Stderr, "buildx unavailable (%v); falling back to 'docker build'.\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "buildx unavailable; falling back to 'docker build'.")
		}
	}
	return runClassicBuild(tag, dockerfilePath, contextDir, opts.ExtraArgs)
}

func runClassicBuild(tag, dockerfilePath, contextDir string, args []string) error {
	argv := []string{"build", "-t", tag, "-f", dockerfilePath}
	argv = append(argv, args...)
	argv = append(argv, contextDir)
	cmd := exec.Command("docker", argv...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "DOCKER_BUILDKIT=1")
	return cmd.Run()
}

func runBuildx(tag, dockerfilePath, contextDir string, opts BuildOptions) error {
	argv := []string{"buildx", "build", "--load", "-t", tag, "-f", dockerfilePath}
	if builder := strings.TrimSpace(opts.Builder); builder != "" {
		argv = append(argv, "--builder", builder)
	}
	argv = append(argv, opts.ExtraArgs...)
	argv = append(argv, contextDir)
	cmd := exec.Command("docker", argv...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "DOCKER_BUILDKIT=1")
	return cmd.Run()
}

var (
	buildxOnce sync.Once
	buildxOK   bool
	buildxErr  error
)

func buildxAvailable() bool {
	buildxOnce.Do(func() {
		cmd := exec.Command("docker", "buildx", "version")
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		buildxErr = cmd.Run()
		buildxOK = buildxErr == nil
	})
	return buildxOK
}

func buildxError() error {
	buildxAvailable()
	return buildxErr
}

func isTruthyEnv(key string) bool {
	val := strings.TrimSpace(os.Getenv(key))
	switch strings.ToLower(val) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func ImageExists(tag string) bool {
	out, _ := exec.Command("bash", "-lc", "docker images -q "+shellEscape(tag)).Output()
	return strings.TrimSpace(string(out)) != ""
}

func RemoveImage(tag string) error {
	cmd := exec.Command("docker", "rmi", tag)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// TagImage applies a new tag to an existing image (docker tag src dst)
func TagImage(srcTag, dstTag string) error {
	cmd := exec.Command("docker", "tag", srcTag, dstTag)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func Start(name string) error {
	cmd := exec.Command("docker", "start", name)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func RunDetached(name, workdir, image string, hostPort, containerPort int, labels map[string]string, envs map[string]string) error {
	args := []string{"run", "-d",
		"--name", name,
		"-w", workdir,
		"-p", fmt.Sprintf("%d:%d", hostPort, containerPort),
	}
	// Apply environment variables
	for k, v := range envs {
		if strings.TrimSpace(k) == "" || strings.Contains(k, "\n") {
			continue
		}
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	// Apply labels for provenance and discovery
	for k, v := range labels {
		if strings.TrimSpace(k) == "" || strings.Contains(k, "\n") {
			continue
		}
		args = append(args, "--label", fmt.Sprintf("%s=%s", k, v))
	}
	args = append(args, image, "--sysctl", "kernel.unprivileged_userns_clone=1")
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func ExecInteractive(name, workdir string, envs []string, argv []string) error {
	args := []string{"exec", "-i", "--user", "discourse", "-w", workdir}
	// Add -t only when stdout is a TTY
	if term.IsTerminal(int(os.Stdout.Fd())) {
		args = append([]string{"exec", "-t"}, args[1:]...)
	}
	for _, e := range envs {
		args = append(args, "-e", e)
	}
	args = append(args, name)
	args = append(args, argv...)
	cmd := exec.Command("docker", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// ExecInteractiveAsRoot runs an interactive command inside the container as root.
func ExecInteractiveAsRoot(name, workdir string, envs []string, argv []string) error {
	args := []string{"exec", "-i", "--user", "root", "-w", workdir}
	// Add -t only when stdout is a TTY
	if term.IsTerminal(int(os.Stdout.Fd())) {
		args = append([]string{"exec", "-t"}, args[1:]...)
	}
	for _, e := range envs {
		args = append(args, "-e", e)
	}
	args = append(args, name)
	args = append(args, argv...)
	cmd := exec.Command("docker", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func ExecOutput(name, workdir string, argv []string) (string, error) {
	args := []string{"exec", "--user", "discourse", "-w", workdir}
	args = append(args, name)
	args = append(args, argv...)
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ExecAsRoot runs a command inside the container as root, returning combined output.
func ExecAsRoot(name, workdir string, argv []string) (string, error) {
	args := []string{"exec", "--user", "root", "-w", workdir}
	args = append(args, name)
	args = append(args, argv...)
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func CopyFromContainer(name, srcInContainer, dstOnHost string) error {
	cmd := exec.Command("docker", "cp", fmt.Sprintf("%s:%s", name, srcInContainer), dstOnHost)
	if isTruthyEnv("DV_VERBOSE") {
		cmd.Stdout = os.Stdout
	} else {
		cmd.Stdout = io.Discard
	}
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func CopyToContainer(name, srcOnHost, dstInContainer string) error {
	cmd := exec.Command("docker", "cp", srcOnHost, fmt.Sprintf("%s:%s", name, dstInContainer))
	if isTruthyEnv("DV_VERBOSE") {
		cmd.Stdout = os.Stdout
	} else {
		cmd.Stdout = io.Discard
	}
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// CopyToContainerWithOwnership copies a file or directory into a container and
// sets its ownership to discourse:discourse. If recursive is true, ownership is
// set recursively (useful for directories).
func CopyToContainerWithOwnership(name, srcOnHost, dstInContainer string, recursive bool) error {
	if err := CopyToContainer(name, srcOnHost, dstInContainer); err != nil {
		return err
	}

	chownArgs := []string{"chown"}
	if recursive {
		chownArgs = append(chownArgs, "-R")
	}
	chownArgs = append(chownArgs, "discourse:discourse", dstInContainer)

	if _, err := ExecAsRoot(name, "/", chownArgs); err != nil {
		return fmt.Errorf("failed to set ownership on %s: %w", dstInContainer, err)
	}
	return nil
}

func shellEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		if r == '\\' || r == '"' || r == '$' || r == '`' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
