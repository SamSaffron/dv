package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAnthropicConnectorFetch_FiltersAndPrices(t *testing.T) {
	t.Parallel()

	var gotAuth, gotVersion string
	client := &http.Client{Transport: stubTransport{fn: func(r *http.Request) *http.Response {
		gotAuth = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		body := `{
  "data": [
    {
      "id": "claude-3-5-sonnet-20241022",
      "display_name": "Claude 3.5 Sonnet",
      "created_at": "2024-10-22T00:00:00Z"
    },
    {
      "id": "claude-3-opus-20240229",
      "display_name": "Claude 3 Opus",
      "created_at": "2024-02-29T00:00:00Z"
    },
    {
      "id": "some-other-model",
      "display_name": "Other Model"
    }
  ]
}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}
	}}}

	conn := &anthropicConnector{}
	models, _, err := conn.fetch(context.Background(), client, map[string]string{
		"ANTHROPIC_API_KEY": "test-key",
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotAuth != "test-key" {
		t.Fatalf("expected x-api-key to be set, got %q", gotAuth)
	}
	if gotVersion != "2023-06-01" {
		t.Fatalf("expected anthropic-version header, got %q", gotVersion)
	}

	// Should filter to only Claude models
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	foundSonnet := false
	foundOpus := false
	for _, m := range models {
		switch m.ID {
		case "claude-3-5-sonnet-20241022":
			foundSonnet = true
			if m.Provider != "anthropic" {
				t.Fatalf("expected provider anthropic, got %q", m.Provider)
			}
			if m.Family != "claude" {
				t.Fatalf("expected family claude, got %q", m.Family)
			}
			// Check pricing from hardcoded table
			if m.InputCost != 3.0 {
				t.Fatalf("expected input cost 3.0, got %f", m.InputCost)
			}
			if m.OutputCost != 15.0 {
				t.Fatalf("expected output cost 15.0, got %f", m.OutputCost)
			}
			if m.ContextTokens != 200000 {
				t.Fatalf("expected context tokens 200000, got %d", m.ContextTokens)
			}
			if !m.SupportsVision {
				t.Fatal("expected Claude 3.5 Sonnet to support vision")
			}
		case "claude-3-opus-20240229":
			foundOpus = true
			if m.InputCost != 15.0 {
				t.Fatalf("expected input cost 15.0 for Opus, got %f", m.InputCost)
			}
			if m.OutputCost != 75.0 {
				t.Fatalf("expected output cost 75.0 for Opus, got %f", m.OutputCost)
			}
		}
	}
	if !foundSonnet {
		t.Fatal("expected claude-3-5-sonnet to be included")
	}
	if !foundOpus {
		t.Fatal("expected claude-3-opus to be included")
	}
}

func TestAnthropicConnectorFetch_Unauthorized(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: stubTransport{fn: func(r *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Status:     http.StatusText(http.StatusUnauthorized),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("unauthorized")),
			Request:    r,
		}
	}}}

	conn := &anthropicConnector{}
	_, _, err := conn.fetch(context.Background(), client, map[string]string{
		"ANTHROPIC_API_KEY": "bad-key",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnthropicConnectorFetch_Forbidden(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: stubTransport{fn: func(r *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Status:     http.StatusText(http.StatusForbidden),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("forbidden")),
			Request:    r,
		}
	}}}

	conn := &anthropicConnector{}
	_, _, err := conn.fetch(context.Background(), client, map[string]string{
		"ANTHROPIC_API_KEY": "bad-key",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIsInterestingAnthropicModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id       string
		expected bool
	}{
		{"claude-3-5-sonnet-20241022", true},
		{"claude-3-opus-20240229", true},
		{"claude-3-haiku-20240307", true},
		{"CLAUDE-3-sonnet", true}, // case insensitive
		{"some-other-model", false},
		{"gpt-4", false},
	}

	for _, tc := range tests {
		got := isInterestingAnthropicModel(tc.id)
		if got != tc.expected {
			t.Errorf("isInterestingAnthropicModel(%q) = %v, want %v", tc.id, got, tc.expected)
		}
	}
}

func TestLookupAnthropicPricing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id            string
		expectInput   float64
		expectOutput  float64
		expectContext int
	}{
		{"claude-3-5-sonnet-20241022", 3.0, 15.0, 200000},
		{"claude-3-opus-20240229", 15.0, 75.0, 200000},
		{"claude-3-haiku-20240307", 0.25, 1.25, 200000},
		// Fallback to defaults for unknown models
		{"claude-unknown-model", 3.0, 15.0, 200000},
	}

	for _, tc := range tests {
		pricing := lookupAnthropicPricing(tc.id)
		if pricing.InputCost != tc.expectInput {
			t.Errorf("lookupAnthropicPricing(%q) input = %f, want %f", tc.id, pricing.InputCost, tc.expectInput)
		}
		if pricing.OutputCost != tc.expectOutput {
			t.Errorf("lookupAnthropicPricing(%q) output = %f, want %f", tc.id, pricing.OutputCost, tc.expectOutput)
		}
		if pricing.ContextTokens != tc.expectContext {
			t.Errorf("lookupAnthropicPricing(%q) context = %d, want %d", tc.id, pricing.ContextTokens, tc.expectContext)
		}
	}
}

func TestFormatAnthropicModelName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id       string
		expected string
	}{
		{"claude-3-5-sonnet-20241022", "Claude 3 5 sonnet"},
		{"claude-3-opus-20240229", "Claude 3 opus"},
		{"claude", "claude"}, // returns as-is when < 2 parts
		{"", ""},
	}

	for _, tc := range tests {
		got := formatAnthropicModelName(tc.id)
		if got != tc.expected {
			t.Errorf("formatAnthropicModelName(%q) = %q, want %q", tc.id, got, tc.expected)
		}
	}
}

func TestIsNumeric(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s        string
		expected bool
	}{
		{"20241022", true},
		{"12345678", true},
		{"", true}, // empty string has no non-numeric chars
		{"123abc", false},
		{"abc", false},
		{"12-34", false},
	}

	for _, tc := range tests {
		got := isNumeric(tc.s)
		if got != tc.expected {
			t.Errorf("isNumeric(%q) = %v, want %v", tc.s, got, tc.expected)
		}
	}
}
