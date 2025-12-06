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
puts key.key
puts admin.username
`, opts.Description)

	cmd := fmt.Sprintf("cd %s && RAILS_ENV=development bundle exec rails runner - <<'RUBY'\n%s\nRUBY",
		shellQuote(opts.Workdir), rubyScript)

	var key, username string
	var lastErr error

	for attempt := 1; attempt <= KeyRetryAttempts; attempt++ {
		verboseLog("Attempt %d/%d...", attempt, KeyRetryAttempts)

		out, err := docker.ExecOutput(opts.ContainerName, opts.Workdir, []string{"bash", "-lc", cmd})
		if err != nil {
			lastErr = fmt.Errorf("rails runner failed: %w\nOutput: %s", err, out)
			if attempt < KeyRetryAttempts {
				verboseLog("Failed, retrying in %s: %v", KeyRetryDelay, lastErr)
				time.Sleep(KeyRetryDelay)
				continue
			}
			return nil, lastErr
		}

		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) < 2 {
			lastErr = fmt.Errorf("unexpected rails output: %s", out)
			if attempt < KeyRetryAttempts {
				verboseLog("Failed, retrying in %s: %v", KeyRetryDelay, lastErr)
				time.Sleep(KeyRetryDelay)
				continue
			}
			return nil, lastErr
		}

		// Get the last two non-empty lines
		key = strings.TrimSpace(lines[len(lines)-2])
		username = strings.TrimSpace(lines[len(lines)-1])

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

		if username == "" {
			lastErr = fmt.Errorf("empty username returned")
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
func ReadCachedKey(containerName, workdir, keyPath string) (string, error) {
	if !docker.Running(containerName) {
		return "", fmt.Errorf("container not running")
	}

	readCmd := fmt.Sprintf("cat %s 2>/dev/null", shellQuote(keyPath))
	out, err := docker.ExecOutput(containerName, workdir, []string{"bash", "-c", readCmd})
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
func SaveKeyToContainer(containerName, workdir, keyPath, content string) error {
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
	_, err := docker.ExecOutput(containerName, workdir, []string{"bash", "-c", saveCmd})
	return err
}

// EnsureAPIKeyForService generates or retrieves an API key for a specific service.
// This is the main entry point for theme/MCP key management.
func EnsureAPIKeyForService(containerName, workdir, description, keyPath string, verbose bool) (string, string, error) {
	verboseLog := func(format string, args ...interface{}) {
		if verbose {
			fmt.Printf("[discourse-api] "+format+"\n", args...)
		}
	}

	// Try reading cached key first
	if key, err := ReadCachedKey(containerName, workdir, keyPath); err == nil && key != "" {
		verboseLog("Using cached key from %s", keyPath)
		// We don't have the username cached, but for most uses (theme watcher) we don't need it
		return key, "", nil
	}

	// Generate new key
	generated, err := GenerateAPIKey(GenerateAPIKeyOptions{
		ContainerName: containerName,
		Workdir:       workdir,
		Description:   description,
		Verbose:       verbose,
	})
	if err != nil {
		return "", "", err
	}

	// Cache the key
	if err := SaveKeyToContainer(containerName, workdir, keyPath, generated.Key+"\n"); err != nil {
		verboseLog("Warning: failed to cache key: %v", err)
		// Non-fatal
	}

	return generated.Key, generated.Username, nil
}
