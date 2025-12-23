package docker

import (
	"bytes"
	"encoding/json"
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
	if isTruthyEnv("DV_VERBOSE") {
		fmt.Fprintf(os.Stderr, "Running: docker stop %s\n", name)
	}
	cmd := exec.Command("docker", "stop", name)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func Remove(name string) error {
	if isTruthyEnv("DV_VERBOSE") {
		fmt.Fprintf(os.Stderr, "Running: docker rm %s\n", name)
	}
	cmd := exec.Command("docker", "rm", name)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func RemoveForce(name string) error {
	if isTruthyEnv("DV_VERBOSE") {
		fmt.Fprintf(os.Stderr, "Running: docker rm -f %s\n", name)
	}
	cmd := exec.Command("docker", "rm", "-f", name)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func Rename(oldName, newName string) error {
	if isTruthyEnv("DV_VERBOSE") {
		fmt.Fprintf(os.Stderr, "Running: docker rename %s %s\n", oldName, newName)
	}
	cmd := exec.Command("docker", "rename", oldName, newName)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// Pull applies to an image ref (repo:tag or repo@digest)
func Pull(ref string) error {
	if isTruthyEnv("DV_VERBOSE") {
		fmt.Fprintf(os.Stderr, "Running: docker pull %s\n", ref)
	}
	cmd := exec.Command("docker", "pull", ref)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// PullBaseImages parses the Dockerfile at path and attempts to pull all unique
// images found in FROM instructions. It ignores images that refer to
// build stages (AS ...). It prints warnings to stderr on failure but returns nil.
func PullBaseImages(dockerfilePath string, out io.Writer) {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return
	}

	stages := make(map[string]bool)
	var toPull []string

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		if !strings.HasPrefix(upper, "FROM") {
			continue
		}
		fields := strings.Fields(trimmed)
		// FROM [--platform=...] image [AS name]
		idx := 1
		if idx < len(fields) && strings.HasPrefix(fields[idx], "--platform=") {
			idx++
		}
		if idx < len(fields) {
			image := fields[idx]
			if image != "scratch" && !stages[image] && !strings.Contains(image, "$") {
				toPull = append(toPull, image)
			}
			// Check for AS name
			for i := idx + 1; i < len(fields); i++ {
				if strings.ToUpper(fields[i]) == "AS" && i+1 < len(fields) {
					stages[fields[i+1]] = true
					break
				}
			}
		}
	}

	// Pull unique images
	pulled := make(map[string]bool)
	for _, img := range toPull {
		if pulled[img] {
			continue
		}
		fmt.Fprintf(out, "Pulling latest base image %s...\n", img)
		if err := Pull(img); err != nil {
			fmt.Fprintf(out, "Warning: failed to pull %s (%v); continuing with local version if available.\n", img, err)
		}
		pulled[img] = true
	}
}

func Build(tag string, args []string) error {
	argv := []string{"build", "-t", tag}
	argv = append(argv, args...)
	argv = append(argv, ".")
	if isTruthyEnv("DV_VERBOSE") {
		fmt.Fprintf(os.Stderr, "Running: docker %s\n", strings.Join(argv, " "))
	}
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
	if isTruthyEnv("DV_VERBOSE") {
		fmt.Fprintf(os.Stderr, "Running: docker %s\n", strings.Join(argv, " "))
	}
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
	if isTruthyEnv("DV_VERBOSE") {
		fmt.Fprintf(os.Stderr, "Running: docker %s\n", strings.Join(argv, " "))
	}
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
	if isTruthyEnv("DV_VERBOSE") {
		fmt.Fprintf(os.Stderr, "Running: docker rmi %s\n", tag)
	}
	cmd := exec.Command("docker", "rmi", tag)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// RemoveImageQuiet removes an image, suppressing output and errors.
// Useful for cleanup where failure is acceptable.
func RemoveImageQuiet(tag string) error {
	if isTruthyEnv("DV_VERBOSE") {
		fmt.Fprintf(os.Stderr, "Running: docker rmi -f %s\n", tag)
	}
	cmd := exec.Command("docker", "rmi", "-f", tag)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run()
}

// TagImage applies a new tag to an existing image (docker tag src dst)
func TagImage(srcTag, dstTag string) error {
	if isTruthyEnv("DV_VERBOSE") {
		fmt.Fprintf(os.Stderr, "Running: docker tag %s %s\n", srcTag, dstTag)
	}
	cmd := exec.Command("docker", "tag", srcTag, dstTag)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func Start(name string) error {
	if isTruthyEnv("DV_VERBOSE") {
		fmt.Fprintf(os.Stderr, "Running: docker start %s\n", name)
	}
	cmd := exec.Command("docker", "start", name)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// ContainerIP returns the IP address of a running container on the default bridge network.
func ContainerIP(name string) (string, error) {
	out, err := exec.Command("docker", "inspect", name, "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}").Output()
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" {
		return "", fmt.Errorf("container %s has no IP address", name)
	}
	return ip, nil
}

func RunDetached(name, workdir, image string, hostPort, containerPort int, labels map[string]string, envs map[string]string, extraHosts []string) error {
	args := []string{"run", "-d",
		"--name", name,
		"-w", workdir,
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, containerPort),
	}
	if isTruthyEnv("DV_VERBOSE") {
		fmt.Fprintf(os.Stderr, "Running: docker %s\n", strings.Join(args, " "))
	}
	// Apply extra hosts
	for _, h := range extraHosts {
		args = append(args, "--add-host", h)
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

func Labels(name string) (map[string]string, error) {
	cmd := exec.Command("docker", "inspect", "-f", "{{json .Config.Labels}}", name)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	if err := json.Unmarshal(out, &labels); err != nil {
		return nil, err
	}
	if labels == nil {
		labels = map[string]string{}
	}
	return labels, nil
}

func UpdateLabels(name string, labels map[string]string) error {
	if len(labels) == 0 {
		return nil
	}
	args := []string{"update"}
	for k, v := range labels {
		args = append(args, "--label-add", fmt.Sprintf("%s=%s", k, v))
	}
	args = append(args, name)
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// GetContainerHostPort returns the host port mapped to the given container port.
// Returns 0 if no mapping found or container doesn't exist.
// Works on both running and stopped containers by inspecting HostConfig.
func GetContainerHostPort(name string, containerPort int) (int, error) {
	// Use docker inspect to get port bindings - works even when container is stopped
	portKey := fmt.Sprintf("%d/tcp", containerPort)
	format := fmt.Sprintf("{{(index .HostConfig.PortBindings \"%s\" 0).HostPort}}", portKey)
	out, err := exec.Command("docker", "inspect", "-f", format, name).Output()
	if err != nil {
		return 0, err
	}
	portStr := strings.TrimSpace(string(out))
	if portStr == "" || portStr == "<no value>" {
		return 0, fmt.Errorf("no port mapping found")
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return 0, fmt.Errorf("invalid port number: %s", portStr)
	}
	return port, nil
}

// CommitContainer creates an image from a container's current filesystem state.
func CommitContainer(name, imageTag string) error {
	cmd := exec.Command("docker", "commit", name, imageTag)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// AllocatedPorts returns a set of all host ports currently allocated by Docker
// containers (running or stopped). It uses a more robust approach by listing
// all containers and inspecting them individually to avoid failing on a single
// malformed container.
func AllocatedPorts() (map[int]bool, error) {
	// 1. Get all container IDs
	out, err := exec.Command("docker", "ps", "-aq").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return make(map[int]bool), nil
	}

	// 2. Inspect all containers at once with a template that handles multiple ports
	format := "{{range $p, $conf := .HostConfig.PortBindings}}{{(index $conf 0).HostPort}} {{end}}"
	args := append([]string{"inspect", "-f", format}, ids...)
	out, err = exec.Command("docker", args...).Output()
	if err != nil {
		// If batch inspect fails, fallback to one-by-one to be resilient
		return allocatedPortsOneByOne(ids)
	}

	ports := make(map[int]bool)
	fields := strings.Fields(string(out))
	for _, f := range fields {
		var p int
		if _, err := fmt.Sscanf(f, "%d", &p); err == nil {
			ports[p] = true
		}
	}
	return ports, nil
}

func allocatedPortsOneByOne(ids []string) (map[int]bool, error) {
	ports := make(map[int]bool)
	format := "{{range $p, $conf := .HostConfig.PortBindings}}{{(index $conf 0).HostPort}} {{end}}"
	for _, id := range ids {
		out, err := exec.Command("docker", "inspect", "-f", format, id).Output()
		if err != nil {
			continue // skip malformed or missing containers
		}
		fields := strings.Fields(string(out))
		for _, f := range fields {
			var p int
			if _, err := fmt.Sscanf(f, "%d", &p); err == nil {
				ports[p] = true
			}
		}
	}
	return ports, nil
}

// GetContainerWorkdir returns the working directory configured for a container.
func GetContainerWorkdir(name string) (string, error) {
	out, err := exec.Command("docker", "inspect", "-f", "{{.Config.WorkingDir}}", name).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// GetContainerEnv returns environment variables set on a container as a map.
func GetContainerEnv(name string) (map[string]string, error) {
	out, err := exec.Command("docker", "inspect", "-f", "{{json .Config.Env}}", name).Output()
	if err != nil {
		return nil, err
	}
	var envList []string
	if err := json.Unmarshal(out, &envList); err != nil {
		return nil, err
	}
	envMap := make(map[string]string)
	for _, e := range envList {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	return envMap, nil
}
