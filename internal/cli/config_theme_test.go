package cli

import "testing"

func TestThemeDirSlug(t *testing.T) {
	tests := map[string]string{
		"My Theme":              "my-theme",
		"already-kebab":         "already-kebab",
		"   spaces   ":          "spaces",
		"Symbols*&^":            "symbols",
		"":                      "theme",
		"Ünicode Friendly Name": "ünicode-friendly-name",
	}
	for input, expected := range tests {
		if got := themeDirSlug(input); got != expected {
			t.Fatalf("themeDirSlug(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestNormalizeThemeRepo(t *testing.T) {
	tests := []struct {
		in       string
		url      string
		basename string
	}{
		{"discourse/new-theme", "https://github.com/discourse/new-theme.git", "new-theme"},
		{"custom-theme", "https://github.com/discourse/custom-theme.git", "custom-theme"},
		{"https://example.com/foo/bar.git", "https://example.com/foo/bar.git", "bar"},
		{"git@github.com:foo/bar.git", "git@github.com:foo/bar.git", "bar"},
	}
	for _, tt := range tests {
		url, name := normalizeThemeRepo(tt.in)
		if url != tt.url || name != tt.basename {
			t.Fatalf("normalizeThemeRepo(%q) = (%q,%q) want (%q,%q)", tt.in, url, name, tt.url, tt.basename)
		}
	}
}
