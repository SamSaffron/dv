package discourse

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"dv/internal/docker"
)

const (
	// KeyRetryAttempts is the number of times to retry key generation
	KeyRetryAttempts = 7
	// KeyRetryDelay is the delay between key generation attempts
	KeyRetryDelay = 5 * time.Second
)

// GenerateAPIKeyOptions configures API key generation
type GenerateAPIKeyOptions struct {
	ContainerName string
	Workdir       string
	Description   string
	Envs          docker.Envs
	Verbose       bool
}

// GeneratedKey holds the result of API key generation
type GeneratedKey struct {
	Key      string
	Username string
}

// GenerateAPIKey creates a new Discourse API key with the given description.
// It will revoke any existing keys with the same description first.
// This is the centralized key generation function used by all dv components.
func GenerateAPIKey(opts GenerateAPIKeyOptions) (*GeneratedKey, error) {
	if !docker.Running(opts.ContainerName) {
		return nil, fmt.Errorf("container %s not running - run 'dv start' first", opts.ContainerName)
	}

	if opts.Description == "" {
		opts.Description = APIKeyDescription
	}

	verboseLog := func(format string, args ...interface{}) {
		if opts.Verbose {
			fmt.Printf("[discourse-api] "+format+"\n", args...)
		}
	}

	verboseLog("Generating API key with description: %s", opts.Description)

	// Ruby script to create or find existing API key
	// Uses DV_API_KEY: and DV_USERNAME: markers to be robust against warnings/noise in stdout
	rubyScript := fmt.Sprintf(`
require "json"
ActiveRecord::Base.logger = nil
Rails.logger.level = 4

desc = %q
admin = User.find_by(id: -1) || User.where(admin: true).order(:id).first
raise "No admin user found. Seed the database first." if admin.nil?

# Revoke any existing keys with this description
ApiKey.where(description: desc).update_all(revoked_at: Time.current)

# Create new key
key = ApiKey.create!(
  user: admin,
  description: desc,
  created_by_id: admin.id
)

STDOUT.sync = true
puts "DV_API_KEY:#{key.key}"
puts "DV_USERNAME:#{admin.username}"
`, opts.Description)

	cmd := fmt.Sprintf("cd %s && RAILS_ENV=development bundle exec rails runner - <<'RUBY'\n%s\nRUBY",
		shellQuote(opts.Workdir), rubyScript)

	verboseLog("Running command: bash -lc %q", cmd)

	var lastErr error

	for attempt := 1; attempt <= KeyRetryAttempts; attempt++ {
		verboseLog("Attempt %d/%d...", attempt, KeyRetryAttempts)

		// Reset per attempt to avoid carrying over partial results
		var key, username string

		out, err := docker.ExecCombinedOutput(opts.ContainerName, opts.Workdir, opts.Envs, []string{"bash", "-lc", cmd})
		verboseLog("Rails runner output (%d bytes, markers: key=%t, user=%t)", len(out), strings.Contains(out, "DV_API_KEY:"), strings.Contains(out, "DV_USERNAME:"))
		if err != nil {
			lastErr = fmt.Errorf("rails runner failed: %w\nOutput: %s", err, out)
			if attempt < KeyRetryAttempts {
				verboseLog("Failed, retrying in %s: %v", KeyRetryDelay, lastErr)
				time.Sleep(KeyRetryDelay)
				continue
			}
			return nil, lastErr
		}

		// Parse output looking for DV_API_KEY: and DV_USERNAME: markers
		// This is robust against warnings/noise that plugins may emit during Rails init
		lines := strings.Split(out, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "DV_API_KEY:") {
				key = strings.TrimPrefix(line, "DV_API_KEY:")
			} else if strings.HasPrefix(line, "DV_USERNAME:") {
				username = strings.TrimPrefix(line, "DV_USERNAME:")
			}
		}

		if key == "" || username == "" {
			lastErr = fmt.Errorf("missing DV_API_KEY or DV_USERNAME markers in output: %q", out)
			if attempt < KeyRetryAttempts {
				verboseLog("Failed, retrying in %s: %v", KeyRetryDelay, lastErr)
				time.Sleep(KeyRetryDelay)
				continue
			}
			return nil, lastErr
		}

		// Validate key format (should be hex, 32-64 chars)
		keyRe := regexp.MustCompile(`^[0-9a-f]{32,64}$`)
		if !keyRe.MatchString(key) {
			lastErr = fmt.Errorf("invalid API key format: %q", key)
			if attempt < KeyRetryAttempts {
				verboseLog("Failed, retrying in %s: %v", KeyRetryDelay, lastErr)
				time.Sleep(KeyRetryDelay)
				continue
			}
			return nil, lastErr
		}

		verboseLog("Successfully generated key for user %s", username)
		return &GeneratedKey{Key: key, Username: username}, nil
	}

	return nil, lastErr
}

// ReadCachedKey reads an API key from a container file path
func ReadCachedKey(containerName, workdir, keyPath string, envs docker.Envs) (string, error) {
	if !docker.Running(containerName) {
		return "", fmt.Errorf("container not running")
	}

	readCmd := fmt.Sprintf("cat %s 2>/dev/null", shellQuote(keyPath))
	out, err := docker.ExecOutput(containerName, workdir, envs, []string{"bash", "-c", readCmd})
	if err != nil {
		return "", fmt.Errorf("read key file: %w", err)
	}

	key := strings.TrimSpace(out)
	if key == "" {
		return "", fmt.Errorf("empty key file")
	}

	// For multi-line format (key + username), return just the first line
	if lines := strings.Split(key, "\n"); len(lines) > 0 {
		return strings.TrimSpace(lines[0]), nil
	}

	return key, nil
}

// SaveKeyToContainer writes an API key to a container file path
func SaveKeyToContainer(containerName, workdir, keyPath, content string, envs docker.Envs) error {
	if !docker.Running(containerName) {
		return fmt.Errorf("container not running")
	}

	dir := keyPath[:strings.LastIndex(keyPath, "/")]
	saveCmd := fmt.Sprintf(
		"install -d -m 700 %s && printf '%%s' %s > %s && chmod 600 %s",
		shellQuote(dir),
		shellQuote(content),
		shellQuote(keyPath),
		shellQuote(keyPath),
	)
	_, err := docker.ExecOutput(containerName, workdir, envs, []string{"bash", "-c", saveCmd})
	return err
}

// EnsureAPIKeyForService generates or retrieves an API key for a specific service.
// This is the main entry point for theme/MCP key management.
func EnsureAPIKeyForService(containerName, workdir, description, keyPath string, envs docker.Envs, verbose bool) (string, string, error) {
	verboseLog := func(format string, args ...interface{}) {
		if verbose {
			fmt.Printf("[discourse-api] "+format+"\n", args...)
		}
	}

	// Try reading cached key first
	if key, err := ReadCachedKey(containerName, workdir, keyPath, envs); err == nil && key != "" {
		verboseLog("Using cached key from %s", keyPath)
		// We don't have the username cached, but for most uses (theme watcher) we don't need it
		return key, "", nil
	}

	// Generate new key
	generated, err := GenerateAPIKey(GenerateAPIKeyOptions{
		ContainerName: containerName,
		Workdir:       workdir,
		Description:   description,
		Envs:          envs,
		Verbose:       verbose,
	})
	if err != nil {
		return "", "", err
	}

	// Cache the key
	if err := SaveKeyToContainer(containerName, workdir, keyPath, generated.Key+"\n", envs); err != nil {
		verboseLog("Warning: failed to cache key: %v", err)
		// Non-fatal
	}

	return generated.Key, generated.Username, nil
}
