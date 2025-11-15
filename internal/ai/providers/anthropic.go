package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"dv/internal/ai"
)

// Anthropic pricing as of 2025-11 from https://www.claude.com/pricing#api
// Prices are in USD per 1M tokens
type anthropicPricing struct {
	InputCost       float64
	CachedInputCost float64
	OutputCost      float64
	ContextTokens   int
}

var anthropicModelPricing = map[string]anthropicPricing{
	"claude-3-5-sonnet-20241022": {
		InputCost:       3.0,
		CachedInputCost: 0.30,
		OutputCost:      15.0,
		ContextTokens:   200000,
	},
	"claude-3-5-haiku-20241022": {
		InputCost:       1.0,
		CachedInputCost: 0.10,
		OutputCost:      5.0,
		ContextTokens:   200000,
	},
	"claude-3-opus-20240229": {
		InputCost:       15.0,
		CachedInputCost: 1.50,
		OutputCost:      75.0,
		ContextTokens:   200000,
	},
	"claude-3-sonnet-20240229": {
		InputCost:       3.0,
		CachedInputCost: 0.30,
		OutputCost:      15.0,
		ContextTokens:   200000,
	},
	"claude-3-haiku-20240307": {
		InputCost:       0.25,
		CachedInputCost: 0.03,
		OutputCost:      1.25,
		ContextTokens:   200000,
	},
}

type anthropicConnector struct{}

func (c *anthropicConnector) id() string    { return "anthropic" }
func (c *anthropicConnector) title() string { return "Anthropic" }
func (c *anthropicConnector) envKeys() []string {
	return []string{"ANTHROPIC_API_KEY"}
}
func (c *anthropicConnector) hasCredentials(env map[string]string) bool {
	return firstEnv(env, c.envKeys()) != ""
}

func (c *anthropicConnector) fetch(ctx context.Context, client *http.Client, env map[string]string) ([]ai.ProviderModel, time.Time, error) {
	apiKey := firstEnv(env, c.envKeys())
	if apiKey == "" {
		return nil, time.Time{}, errMissingAPIKey
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/v1/models", nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("User-Agent", "dv/ai-config")

	resp, err := client.Do(req)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, time.Time{}, unauthorizedErr("Anthropic")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, time.Time{}, fmt.Errorf("anthropic %s: %s", resp.Status, string(body))
	}

	var root struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		return nil, time.Time{}, err
	}

	now := time.Now()
	models := make([]ai.ProviderModel, 0, len(root.Data))
	for _, raw := range root.Data {
		var obj map[string]interface{}
		if err := json.Unmarshal(raw, &obj); err != nil {
			continue
		}
		id := stringValue(obj["id"])
		if id == "" || !isInterestingAnthropicModel(id) {
			continue
		}

		displayName := stringValue(obj["display_name"])
		if displayName == "" {
			displayName = formatAnthropicModelName(id)
		}

		// Get pricing from hardcoded table
		pricing := lookupAnthropicPricing(id)

		// Check capabilities
		supportsVision := false
		if createdAt := stringValue(obj["created_at"]); createdAt != "" {
			// Claude 3 Opus, Sonnet, and Haiku support vision
			if strings.Contains(strings.ToLower(id), "claude-3") {
				supportsVision = true
			}
		}

		models = append(models, ai.ProviderModel{
			ID:                id,
			DisplayName:       displayName,
			Provider:          "anthropic",
			Family:            "claude",
			Endpoint:          "https://api.anthropic.com/v1/messages",
			Tokenizer:         "DiscourseAi::Tokenizer::AnthropicTokenizer",
			ContextTokens:     pricing.ContextTokens,
			InputCost:         pricing.InputCost,
			CachedInputCost:   pricing.CachedInputCost,
			OutputCost:        pricing.OutputCost,
			SupportsVision:    supportsVision,
			SupportsReasoning: false,
			Description:       displayName,
			Tags:              []string{"anthropic", "claude"},
			UpdatedAt:         now,
			Raw:               obj,
		})
	}
	return models, now, nil
}

func isInterestingAnthropicModel(id string) bool {
	lower := strings.ToLower(id)
	// Only include Claude models
	return strings.HasPrefix(lower, "claude-")
}

func lookupAnthropicPricing(id string) anthropicPricing {
	// Try exact match first
	if pricing, ok := anthropicModelPricing[id]; ok {
		return pricing
	}

	// Try fuzzy matching based on model family
	lower := strings.ToLower(id)
	for key, pricing := range anthropicModelPricing {
		if strings.Contains(lower, strings.ToLower(key)) {
			return pricing
		}
	}

	// Default fallback (use Claude 3.5 Sonnet pricing as default)
	return anthropicPricing{
		InputCost:       3.0,
		CachedInputCost: 0.30,
		OutputCost:      15.0,
		ContextTokens:   200000,
	}
}

func formatAnthropicModelName(id string) string {
	// Convert "claude-3-5-sonnet-20241022" to "Claude 3.5 Sonnet"
	parts := strings.Split(id, "-")
	if len(parts) < 2 {
		return id
	}

	var result []string
	for i, part := range parts {
		// Skip date suffixes (8 digit numbers)
		if len(part) == 8 && isNumeric(part) {
			break
		}
		// Capitalize first letter
		if i == 0 {
			result = append(result, strings.Title(part))
		} else {
			result = append(result, part)
		}
	}
	return strings.Join(result, " ")
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
