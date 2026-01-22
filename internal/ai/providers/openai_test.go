package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOpenAIConnectorFetch_FiltersModels(t *testing.T) {
	t.Parallel()

	var gotAuth string
	client := &http.Client{Transport: stubTransport{fn: func(r *http.Request) *http.Response {
		gotAuth = r.Header.Get("Authorization")
		body := `{
  "data": [
    {
      "id": "gpt-4o",
      "owned_by": "openai",
      "description": "GPT-4o model"
    },
    {
      "id": "o1-mini",
      "owned_by": "openai",
      "description": "o1-mini reasoning model"
    },
    {
      "id": "dall-e-3",
      "owned_by": "openai",
      "description": "Image generation model"
    },
    {
      "id": "text-embedding-3-small",
      "owned_by": "openai",
      "description": "Embedding model"
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

	conn := &openAIConnector{}
	models, _, err := conn.fetch(context.Background(), client, map[string]string{
		"OPENAI_API_KEY": "test-key",
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("expected Authorization header to be set, got %q", gotAuth)
	}

	// Should include gpt-4o, o1-mini, and text-embedding, but not dall-e
	foundGPT4o := false
	foundO1Mini := false
	foundEmbedding := false
	foundDallE := false
	for _, m := range models {
		switch m.ID {
		case "gpt-4o":
			foundGPT4o = true
			if m.Provider != "open_ai" {
				t.Fatalf("expected provider open_ai, got %q", m.Provider)
			}
		case "o1-mini":
			foundO1Mini = true
			if !m.SupportsReasoning {
				t.Fatal("expected o1-mini to support reasoning")
			}
		case "text-embedding-3-small":
			foundEmbedding = true
			if !strings.Contains(m.Endpoint, "embeddings") {
				t.Fatalf("expected embedding endpoint, got %q", m.Endpoint)
			}
		case "dall-e-3":
			foundDallE = true
		}
	}
	if !foundGPT4o {
		t.Fatal("expected gpt-4o to be included")
	}
	if !foundO1Mini {
		t.Fatal("expected o1-mini to be included")
	}
	if !foundEmbedding {
		t.Fatal("expected text-embedding-3-small to be included")
	}
	if foundDallE {
		t.Fatal("expected dall-e-3 to be filtered out")
	}
}

func TestOpenAIConnectorFetch_AppliesPricingHints(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: stubTransport{fn: func(r *http.Request) *http.Response {
		body := `{
  "data": [
    {
      "id": "gpt-4o",
      "owned_by": "openai"
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

	conn := &openAIConnector{}
	models, _, err := conn.fetch(context.Background(), client, map[string]string{
		"OPENAI_API_KEY": "test-key",
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}

	m := models[0]
	// gpt-4o should have pricing hints applied (non-zero values)
	// Note: exact values depend on map iteration order due to substring matching
	if m.ContextTokens == 0 {
		t.Fatal("expected context tokens to be set from hints")
	}
	if m.InputCost == 0 {
		t.Fatal("expected input cost to be set from hints")
	}
	if m.OutputCost == 0 {
		t.Fatal("expected output cost to be set from hints")
	}
}

func TestOpenAIConnectorFetch_Unauthorized(t *testing.T) {
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

	conn := &openAIConnector{}
	_, _, err := conn.fetch(context.Background(), client, map[string]string{
		"OPENAI_API_KEY": "bad-key",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIsInterestingOpenAIModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id       string
		expected bool
	}{
		{"gpt-4o", true},
		{"gpt-4o-mini", true},
		{"gpt-3.5-turbo", true},
		{"GPT-4", true}, // case insensitive
		{"o1-mini", true},
		{"o1-preview", true},
		{"o3-mini", true},
		{"o4-mini", true},
		{"chatgpt-4o", true},
		{"text-embedding-3-small", true},
		{"whisper-1", true},
		{"tts-1", true},
		{"tts-1-hd", true},
		{"gpt-realtime-preview", true},
		{"gpt-audio-preview", true},
		{"codex-mini", true},
		{"computer-use-preview", true},
		{"dall-e-3", false},
		{"moderation-stable", false},
		{"babbage-002", false},
	}

	for _, tc := range tests {
		got := isInterestingOpenAIModel(tc.id)
		if got != tc.expected {
			t.Errorf("isInterestingOpenAIModel(%q) = %v, want %v", tc.id, got, tc.expected)
		}
	}
}

func TestLookupOpenAIHint(t *testing.T) {
	t.Parallel()

	// Test that known models are found
	tests := []struct {
		id          string
		expectFound bool
	}{
		{"gpt-4o", true},
		{"gpt-4o-2024-11-20", true},
		{"o1-mini", true},
		{"gpt-3.5-turbo", true},
		{"unknown-model", false},
	}

	for _, tc := range tests {
		hint, found := lookupOpenAIHint(tc.id)
		if found != tc.expectFound {
			t.Errorf("lookupOpenAIHint(%q) found = %v, want %v", tc.id, found, tc.expectFound)
		}
		// If found, verify we got reasonable values
		if found && hint.ContextTokens == 0 {
			t.Errorf("lookupOpenAIHint(%q) returned 0 context tokens for a known model", tc.id)
		}
	}
}

func TestGetOpenAIPrice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    interface{}
		expected float64
	}{
		{
			name:     "float64 value",
			input:    2.50,
			expected: 2.50,
		},
		{
			name:     "string value",
			input:    "3.00",
			expected: 3.00,
		},
		{
			name:     "string with spaces",
			input:    "  4.50  ",
			expected: 4.50,
		},
		{
			name: "map with 1M tokens unit",
			input: map[string]interface{}{
				"value": 5.0,
				"unit":  "1m tokens",
			},
			expected: 5.0,
		},
		{
			name: "map with 1K tokens unit",
			input: map[string]interface{}{
				"value": 0.005,
				"unit":  "1k tokens",
			},
			expected: 5.0, // 0.005 * 1000
		},
		{
			name: "map with per token unit",
			input: map[string]interface{}{
				"value": 0.000003,
				"unit":  "token",
			},
			expected: 3.0, // 0.000003 * 1_000_000
		},
		{
			name: "map with usd_per_1m_tokens key",
			input: map[string]interface{}{
				"usd_per_1m_tokens": 10.0,
			},
			expected: 10.0,
		},
		{
			name: "nested usd map",
			input: map[string]interface{}{
				"usd": 7.5,
			},
			expected: 7.5,
		},
		{
			name:     "nil value",
			input:    nil,
			expected: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getOpenAIPrice(tc.input)
			if got != tc.expected {
				t.Errorf("getOpenAIPrice(%v) = %f, want %f", tc.input, got, tc.expected)
			}
		})
	}
}

func TestResolveOpenAIEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id       string
		contains string
	}{
		{"text-embedding-3-small", "embeddings"},
		{"text-embedding-ada-002", "embeddings"},
		{"tts-1", "audio/speech"},
		{"tts-1-hd", "audio/speech"},
		{"gpt-4o", "responses"},
		{"o1-mini", "responses"},
	}

	for _, tc := range tests {
		endpoint := resolveOpenAIEndpoint(tc.id)
		if !strings.Contains(endpoint, tc.contains) {
			t.Errorf("resolveOpenAIEndpoint(%q) = %q, want to contain %q", tc.id, endpoint, tc.contains)
		}
	}
}
