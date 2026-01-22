package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOpenRouterConnectorFetch_ParsesModels(t *testing.T) {
	t.Parallel()

	var gotAuth string
	client := &http.Client{Transport: stubTransport{fn: func(r *http.Request) *http.Response {
		gotAuth = r.Header.Get("Authorization")
		body := `{
  "data": [
    {
      "id": "anthropic/claude-3.5-sonnet",
      "name": "Claude 3.5 Sonnet",
      "description": "Anthropic's Claude 3.5 Sonnet model",
      "context_length": 200000,
      "pricing": {
        "prompt": 0.000003,
        "completion": 0.000015
      },
      "capabilities": {
        "vision": true,
        "reasoning": false
      },
      "top_provider": {
        "provider": "anthropic"
      },
      "tags": ["anthropic", "claude"]
    },
    {
      "id": "openai/gpt-4o",
      "name": "GPT-4o",
      "description": "OpenAI GPT-4o model",
      "context_length": 128000,
      "pricing": {
        "prompt": 0.0000025,
        "completion": 0.00001
      },
      "capabilities": {
        "vision": true
      },
      "top_provider": {
        "provider": "openai"
      }
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

	conn := &openRouterConnector{}
	models, _, err := conn.fetch(context.Background(), client, map[string]string{
		"OPENROUTER_API_KEY": "test-key",
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("expected Authorization header to be set, got %q", gotAuth)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	// Check Claude model
	claude := models[0]
	if claude.ID != "anthropic/claude-3.5-sonnet" {
		t.Fatalf("expected first model ID anthropic/claude-3.5-sonnet, got %q", claude.ID)
	}
	if claude.Provider != "open_router" {
		t.Fatalf("expected provider open_router, got %q", claude.Provider)
	}
	if claude.Family != "anthropic" {
		t.Fatalf("expected family anthropic, got %q", claude.Family)
	}
	if claude.ContextTokens != 200000 {
		t.Fatalf("expected context tokens 200000, got %d", claude.ContextTokens)
	}
	if !claude.SupportsVision {
		t.Fatal("expected Claude to support vision")
	}
	// Pricing is converted from per-token to per-1M tokens
	// 0.000003 * 1_000_000 = 3.0
	if claude.InputCost != 3.0 {
		t.Fatalf("expected input cost 3.0, got %f", claude.InputCost)
	}
	// 0.000015 * 1_000_000 = 15.0
	if claude.OutputCost != 15.0 {
		t.Fatalf("expected output cost 15.0, got %f", claude.OutputCost)
	}

	// Check GPT-4o model
	gpt4o := models[1]
	if gpt4o.ID != "openai/gpt-4o" {
		t.Fatalf("expected second model ID openai/gpt-4o, got %q", gpt4o.ID)
	}
	if gpt4o.Family != "openai" {
		t.Fatalf("expected family openai, got %q", gpt4o.Family)
	}
}

func TestOpenRouterConnectorFetch_AlternateAPIKeyEnv(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: stubTransport{fn: func(r *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"data":[]}`)),
			Request:    r,
		}
	}}}

	conn := &openRouterConnector{}
	// Use alternate env key name
	_, _, err := conn.fetch(context.Background(), client, map[string]string{
		"OPENROUTER_KEY": "alternate-key",
	})
	if err != nil {
		t.Fatalf("fetch with OPENROUTER_KEY: %v", err)
	}
}

func TestOpenRouterConnectorFetch_Unauthorized(t *testing.T) {
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

	conn := &openRouterConnector{}
	_, _, err := conn.fetch(context.Background(), client, map[string]string{
		"OPENROUTER_API_KEY": "bad-key",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenRouterConnectorFetch_HandlesMissingFields(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: stubTransport{fn: func(r *http.Request) *http.Response {
		body := `{
  "data": [
    {
      "id": "some/model"
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

	conn := &openRouterConnector{}
	models, _, err := conn.fetch(context.Background(), client, map[string]string{
		"OPENROUTER_API_KEY": "test-key",
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	// Name should fall back to ID when missing
	if models[0].DisplayName != "some/model" {
		t.Fatalf("expected DisplayName to fall back to ID, got %q", models[0].DisplayName)
	}
}

func TestOpenRouterConnectorFetch_AlternatePricingKeys(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: stubTransport{fn: func(r *http.Request) *http.Response {
		body := `{
  "data": [
    {
      "id": "test/model",
      "name": "Test Model",
      "pricing": {
        "input": 0.000002,
        "output": 0.000008,
        "cached": 0.000001
      }
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

	conn := &openRouterConnector{}
	models, _, err := conn.fetch(context.Background(), client, map[string]string{
		"OPENROUTER_API_KEY": "test-key",
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}

	m := models[0]
	// 0.000002 * 1_000_000 = 2.0
	if m.InputCost != 2.0 {
		t.Fatalf("expected input cost 2.0, got %f", m.InputCost)
	}
	// 0.000008 * 1_000_000 = 8.0
	if m.OutputCost != 8.0 {
		t.Fatalf("expected output cost 8.0, got %f", m.OutputCost)
	}
	// 0.000001 * 1_000_000 = 1.0
	if m.CachedInputCost != 1.0 {
		t.Fatalf("expected cached cost 1.0, got %f", m.CachedInputCost)
	}
}

func TestPriceFromValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    interface{}
		expected float64
	}{
		{
			name:     "float64 value",
			input:    0.000003,
			expected: 0.000003,
		},
		{
			name:     "int value",
			input:    5,
			expected: 5.0,
		},
		{
			name:     "string value",
			input:    "0.000005",
			expected: 0.000005,
		},
		{
			name:     "string with dollar sign",
			input:    "$0.000002",
			expected: 0.000002,
		},
		{
			name: "nested usd map",
			input: map[string]interface{}{
				"usd": 0.00001,
			},
			expected: 0.00001,
		},
		{
			name: "nested map search",
			input: map[string]interface{}{
				"price": 0.000007,
			},
			expected: 0.000007,
		},
		{
			name:     "nil value",
			input:    nil,
			expected: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := priceFromValue(tc.input)
			if got != tc.expected {
				t.Errorf("priceFromValue(%v) = %f, want %f", tc.input, got, tc.expected)
			}
		})
	}
}

func TestBoolValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    interface{}
		expected bool
	}{
		{"bool true", true, true},
		{"bool false", false, false},
		{"string true", "true", true},
		{"string TRUE", "TRUE", true},
		{"string 1", "1", true},
		{"string yes", "yes", true},
		{"string y", "y", true},
		{"string false", "false", false},
		{"string 0", "0", false},
		{"string no", "no", false},
		{"nil", nil, false},
		{"int 1", 1, false}, // int not supported, returns false
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := boolValue(tc.input)
			if got != tc.expected {
				t.Errorf("boolValue(%v) = %v, want %v", tc.input, got, tc.expected)
			}
		})
	}
}
