package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type stubTransport struct {
	fn func(*http.Request) *http.Response
}

func (t stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.fn(req), nil
}

func TestGeminiConnectorFetch_FiltersAndDedupes(t *testing.T) {
	t.Parallel()

	var gotAuth string
	client := &http.Client{Transport: stubTransport{fn: func(r *http.Request) *http.Response {
		gotAuth = r.Header.Get("x-goog-api-key")
		status := http.StatusOK
		body := ""

		switch r.URL.Query().Get("pageToken") {
		case "":
			body = `{
  "models": [
    {
      "name": "models/gemini-2.0-flash-001",
      "baseModelId": "gemini-2.0-flash",
      "displayName": "Gemini 2.0 Flash v001",
      "description": "versioned",
      "inputTokenLimit": 999,
      "outputTokenLimit": 111,
      "supportedGenerationMethods": ["generateContent"]
    },
    {
      "name": "models/text-embedding-004",
      "baseModelId": "text-embedding-004",
      "displayName": "Text Embedding 004",
      "supportedGenerationMethods": ["embedContent"]
    }
  ],
  "nextPageToken": "next"
}`
		case "next":
			body = `{
  "models": [
    {
      "name": "models/gemini-2.0-flash",
      "baseModelId": "gemini-2.0-flash",
      "displayName": "Gemini 2.0 Flash",
      "description": "canonical",
      "inputTokenLimit": 1048576,
      "outputTokenLimit": 8192,
      "supportedGenerationMethods": ["generateContent"]
    }
  ]
}`
		default:
			status = http.StatusBadRequest
			body = "unexpected pageToken"
		}

		resp := &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}
		resp.Header.Set("Content-Type", "application/json")
		return resp
	}}}

	conn := &geminiConnector{}
	models, _, err := conn.fetch(context.Background(), client, map[string]string{
		"GEMINI_API_KEY": "k",
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotAuth != "k" {
		t.Fatalf("expected x-goog-api-key to be set, got %q", gotAuth)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	got := models[0]
	if got.ID != "gemini-2.0-flash" {
		t.Fatalf("expected model id gemini-2.0-flash, got %q", got.ID)
	}
	if got.DisplayName != "Gemini 2.0 Flash" {
		t.Fatalf("expected canonical displayName, got %q", got.DisplayName)
	}
	if got.Provider != "google" {
		t.Fatalf("expected provider google, got %q", got.Provider)
	}
	if got.ContextTokens != 1048576 {
		t.Fatalf("expected contextTokens 1048576, got %d", got.ContextTokens)
	}
	if !strings.Contains(got.Endpoint, "/v1beta/models/gemini-2.0-flash") {
		t.Fatalf("expected endpoint to include model base URL, got %q", got.Endpoint)
	}
	if got.Raw == nil || got.Raw["dv_pricing_unknown"] != true {
		t.Fatalf("expected dv_pricing_unknown=true in raw, got %#v", got.Raw)
	}
}

func TestGeminiConnectorFetch_Unauthorized(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: stubTransport{fn: func(r *http.Request) *http.Response {
		resp := &http.Response{
			StatusCode: http.StatusUnauthorized,
			Status:     http.StatusText(http.StatusUnauthorized),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("nope")),
			Request:    r,
		}
		resp.Header.Set("Content-Type", "text/plain")
		return resp
	}}}

	conn := &geminiConnector{}
	_, _, err := conn.fetch(context.Background(), client, map[string]string{
		"GEMINI_API_KEY": "k",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
