package docker

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

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
// a directory. Additional docker build arguments can be supplied via args.
func BuildFrom(tag, dockerfilePath, contextDir string, args []string) error {
	if !filepath.IsAbs(dockerfilePath) {
		// ensure relative dockerfile path is evaluated relative to contextDir
		dockerfilePath = filepath.Join(contextDir, dockerfilePath)
	}
	argv := []string{"build", "-t", tag, "-f", dockerfilePath}
	argv = append(argv, args...)
	argv = append(argv, contextDir)
	cmd := exec.Command("docker", argv...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
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

func Start(name string) error {
	cmd := exec.Command("docker", "start", name)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func RunDetached(name, workdir, image string, hostPort, containerPort int, labels map[string]string) error {
	args := []string{"run", "-d",
		"--name", name,
		"-w", workdir,
		"-p", fmt.Sprintf("%d:%d", hostPort, containerPort),
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
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func CopyToContainer(name, srcOnHost, dstInContainer string) error {
	cmd := exec.Command("docker", "cp", srcOnHost, fmt.Sprintf("%s:%s", name, dstInContainer))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
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
