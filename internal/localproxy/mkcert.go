package localproxy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func TLSPaths(configDir string) (certPath string, keyPath string) {
	tlsDir := filepath.Join(configDir, "local-proxy", "tls")
	return filepath.Join(tlsDir, "localhost.pem"), filepath.Join(tlsDir, "localhost-key.pem")
}

func EnsureMKCertTLS(configDir string) error {
	if _, err := exec.LookPath("mkcert"); err != nil {
		return fmt.Errorf("mkcert not found in PATH (required for --https): %w", err)
	}

	certPath, keyPath := TLSPaths(configDir)
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		return err
	}

	install := exec.Command("mkcert", "-install")
	install.Stdout, install.Stderr = os.Stdout, os.Stderr
	if err := install.Run(); err != nil {
		return fmt.Errorf("mkcert -install failed: %w", err)
	}

	certOK := fileNonEmpty(certPath)
	keyOK := fileNonEmpty(keyPath)
	if certOK && keyOK {
		return nil
	}

	gen := exec.Command("mkcert", "-cert-file", certPath, "-key-file", keyPath, "localhost", "*.dv.localhost")
	gen.Stdout, gen.Stderr = os.Stdout, os.Stderr
	if err := gen.Run(); err != nil {
		return fmt.Errorf("mkcert failed: %w", err)
	}

	if !fileNonEmpty(certPath) || !fileNonEmpty(keyPath) {
		return fmt.Errorf("mkcert did not produce expected cert/key at %s and %s", certPath, keyPath)
	}
	return nil
}

func fileNonEmpty(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular() && info.Size() > 0
}
