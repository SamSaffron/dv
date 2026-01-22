package docker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShellEscape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "simple string",
			input:    "mycontainer",
			expected: "mycontainer",
		},
		{
			name:     "alphanumeric with dashes",
			input:    "my-container-123",
			expected: "my-container-123",
		},
		{
			name:     "backslash escaped",
			input:    `my\container`,
			expected: `my\\container`,
		},
		{
			name:     "double quote escaped",
			input:    `my"container`,
			expected: `my\"container`,
		},
		{
			name:     "dollar sign escaped",
			input:    "my$container",
			expected: `my\$container`,
		},
		{
			name:     "backtick escaped",
			input:    "my`container",
			expected: "my\\`container",
		},
		{
			name:     "multiple special chars",
			input:    `my$"container\test`,
			expected: `my\$\"container\\test`,
		},
		{
			name:     "regex metachar caret",
			input:    "^mycontainer",
			expected: "^mycontainer",
		},
		{
			name:     "regex metachar dollar",
			input:    "mycontainer$",
			expected: `mycontainer\$`,
		},
		{
			name:     "regex metachar dot",
			input:    "my.container",
			expected: "my.container",
		},
		{
			name:     "regex metachar star",
			input:    "my*container",
			expected: "my*container",
		},
		{
			name:     "all escapable chars",
			input:    `\$"` + "`",
			expected: `\\\$\"` + "\\`",
		},
		{
			name:     "unicode characters",
			input:    "container世界",
			expected: "container世界",
		},
		{
			name:     "real injection attempt",
			input:    `"; rm -rf /; "`,
			expected: `\"; rm -rf /; \"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := shellEscape(tt.input)
			if got != tt.expected {
				t.Errorf("shellEscape(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGetIdentityAgent(t *testing.T) {
	// Note: These tests modify HOME and create temp files
	// Run sequentially to avoid interference

	tests := []struct {
		name     string
		config   string
		expected string
	}{
		{
			name:     "empty config",
			config:   "",
			expected: "",
		},
		{
			name:     "no identity agent",
			config:   "Host example.com\n  User git\n",
			expected: "",
		},
		{
			name:     "global identity agent",
			config:   "IdentityAgent /path/to/agent.sock\n\nHost example.com\n  User git\n",
			expected: "/path/to/agent.sock",
		},
		{
			name:     "identity agent in Host wildcard",
			config:   "Host *\n  IdentityAgent /global/agent.sock\n\nHost example.com\n  User git\n",
			expected: "/global/agent.sock",
		},
		{
			name:     "identity agent in specific host ignored",
			config:   "Host example.com\n  IdentityAgent /specific/agent.sock\n  User git\n",
			expected: "",
		},
		{
			name:     "global before specific host",
			config:   "IdentityAgent /global/first.sock\n\nHost specific\n  IdentityAgent /specific/agent.sock\n",
			expected: "/global/first.sock",
		},
		{
			name:     "with comments",
			config:   "# This is a comment\nIdentityAgent /path/to/agent.sock\n# Another comment\n",
			expected: "/path/to/agent.sock",
		},
		{
			name:     "with empty lines",
			config:   "\n\n\nIdentityAgent /path/to/agent.sock\n\n\n",
			expected: "/path/to/agent.sock",
		},
		{
			name:     "quoted path with spaces",
			config:   `IdentityAgent "/path/with spaces/agent.sock"`,
			expected: "/path/with spaces/agent.sock",
		},
		{
			name:     "single quoted path",
			config:   `IdentityAgent '/path/to/agent.sock'`,
			expected: "/path/to/agent.sock",
		},
		{
			name:     "Host * must be exact",
			config:   "Host *.example.com\n  IdentityAgent /wildcard/agent.sock\n",
			expected: "",
		},
		{
			name:     "case insensitive keywords",
			config:   "IDENTITYAGENT /upper/agent.sock\n",
			expected: "/upper/agent.sock",
		},
		{
			name:     "identity agent after non-wildcard Host block",
			config:   "Host specific\n  User git\n\nHost *\n  IdentityAgent /wildcard/agent.sock\n",
			expected: "/wildcard/agent.sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp home directory
			tmpHome := t.TempDir()

			// Create .ssh directory
			sshDir := filepath.Join(tmpHome, ".ssh")
			if err := os.MkdirAll(sshDir, 0700); err != nil {
				t.Fatalf("failed to create .ssh dir: %v", err)
			}

			// Write config file
			configPath := filepath.Join(sshDir, "config")
			if err := os.WriteFile(configPath, []byte(tt.config), 0600); err != nil {
				t.Fatalf("failed to write config: %v", err)
			}

			// Override HOME
			t.Setenv("HOME", tmpHome)

			got := getIdentityAgent()

			// Handle tilde expansion in expected value
			expected := tt.expected
			if len(expected) > 0 && expected[0] == '~' {
				expected = filepath.Join(tmpHome, expected[2:])
			}

			if got != expected {
				t.Errorf("getIdentityAgent() = %q, want %q", got, expected)
			}
		})
	}
}

func TestGetIdentityAgentTildeExpansion(t *testing.T) {
	tmpHome := t.TempDir()

	sshDir := filepath.Join(tmpHome, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatalf("failed to create .ssh dir: %v", err)
	}

	config := "IdentityAgent ~/Library/agent.sock\n"
	configPath := filepath.Join(sshDir, "config")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	t.Setenv("HOME", tmpHome)

	got := getIdentityAgent()
	expected := filepath.Join(tmpHome, "Library/agent.sock")

	if got != expected {
		t.Errorf("getIdentityAgent() with tilde = %q, want %q", got, expected)
	}
}

func TestGetIdentityAgentMissingConfig(t *testing.T) {
	tmpHome := t.TempDir()
	// Don't create .ssh directory

	t.Setenv("HOME", tmpHome)

	got := getIdentityAgent()
	if got != "" {
		t.Errorf("getIdentityAgent() with missing config = %q, want empty", got)
	}
}

func TestIsTruthyEnv(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected bool
	}{
		{"empty", "", false},
		{"1", "1", true},
		{"true", "true", true},
		{"TRUE", "TRUE", true},
		{"yes", "yes", true},
		{"on", "on", true},
		{"0", "0", false},
		{"false", "false", false},
		{"no", "no", false},
		{"off", "off", false},
		{"random", "random", false},
		{"whitespace 1", "  1  ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_DOCKER_TRUTHY_" + tt.name
			t.Setenv(key, tt.value)
			got := isTruthyEnv(key)
			if got != tt.expected {
				t.Errorf("isTruthyEnv(%q) with value %q = %v, want %v", key, tt.value, got, tt.expected)
			}
		})
	}
}
