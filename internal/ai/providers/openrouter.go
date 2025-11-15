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

type openRouterConnector struct{}

func (c *openRouterConnector) id() string    { return "openrouter" }
func (c *openRouterConnector) title() string { return "OpenRouter" }
func (c *openRouterConnector) envKeys() []string {
	return []string{"OPENROUTER_API_KEY", "OPENROUTER_KEY"}
}
func (c *openRouterConnector) hasCredentials(env map[string]string) bool {
	return firstEnv(env, c.envKeys()) != ""
}

func (c *openRouterConnector) fetch(ctx context.Context, client *http.Client, env map[string]string) ([]ai.ProviderModel, time.Time, error) {
	apiKey := firstEnv(env, c.envKeys())
	if apiKey == "" {
		return nil, time.Time{}, errMissingAPIKey
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "dv/ai-config")

	resp, err := client.Do(req)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, time.Time{}, unauthorizedErr("OpenRouter")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, time.Time{}, fmt.Errorf("openrouter %s: %s", resp.Status, string(body))
	}

	var root struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		return nil, time.Time{}, err
	}

	now := time.Now()
	models := make([]ai.ProviderModel, 0, len(root.Data))
	skippedCount := 0
	for _, raw := range root.Data {
		var obj map[string]interface{}
		if err := json.Unmarshal(raw, &obj); err != nil {
			skippedCount++
			continue
		}
		id := stringValue(obj["id"])
		name := stringValue(obj["name"])
		if id == "" {
			skippedCount++
			continue
		}
		// Use ID as display name if name is missing
		if name == "" {
			name = id
		}
		desc := stringValue(obj["description"])
		contextTokens := int(floatValue(obj["context_length"]))

		var inputCost, outputCost, cachedCost float64
		if pricing, ok := obj["pricing"].(map[string]interface{}); ok {
			inputCost = priceFromValue(pricing["prompt"])
			if inputCost == 0 {
				inputCost = priceFromValue(pricing["input"])
			}
			outputCost = priceFromValue(pricing["completion"])
			if outputCost == 0 {
				outputCost = priceFromValue(pricing["output"])
			}
			cachedCost = priceFromValue(pricing["cached_prompt"])
			if cachedCost == 0 {
				cachedCost = priceFromValue(pricing["cached"])
			}
		}
		// API returns USD per token; convert to per 1M for Discourse UI.
		inputCost *= 1_000_000
		outputCost *= 1_000_000
		cachedCost *= 1_000_000

		var tags []string
		if rawTags, ok := obj["tags"].([]interface{}); ok {
			for _, t := range rawTags {
				if v := stringValue(t); v != "" {
					tags = append(tags, v)
				}
			}
		}

		supportsVision := false
		supportsReasoning := false
		if caps, ok := obj["capabilities"].(map[string]interface{}); ok {
			supportsVision = boolValue(caps["vision"])
			supportsReasoning = boolValue(caps["reasoning"])
		}

		family := ""
		if top, ok := obj["top_provider"].(map[string]interface{}); ok {
			family = stringValue(top["provider"])
		}

		model := ai.ProviderModel{
			ID:                id,
			DisplayName:       name,
			Provider:          "open_router",
			Family:            family,
			Endpoint:          "https://openrouter.ai/api/v1/chat/completions",
			Tokenizer:         "DiscourseAi::Tokenizer::OpenAiTokenizer",
			ContextTokens:     contextTokens,
			InputCost:         inputCost,
			CachedInputCost:   cachedCost,
			OutputCost:        outputCost,
			SupportsVision:    supportsVision,
			SupportsReasoning: supportsReasoning,
			Description:       desc,
			Tags:              tags,
			UpdatedAt:         now,
			Raw:               obj,
		}

		// Debug: Log free models
		if strings.Contains(strings.ToLower(id), "minimax") || strings.Contains(strings.ToLower(name), "minimax") {
			// This is a MiniMax model - let's see if it's being included
		}

		models = append(models, model)
	}

	// Could add logging here about skipped models if needed
	_ = skippedCount

	return models, now, nil
}

func priceFromValue(v interface{}) float64 {
	switch val := v.(type) {
	case map[string]interface{}:
		if usd, ok := val["usd"]; ok {
			return priceFromValue(usd)
		}
		for _, nested := range val {
			if price := priceFromValue(nested); price > 0 {
				return price
			}
		}
	case float64:
		return val
	case int:
		return float64(val)
	case json.Number:
		f, _ := val.Float64()
		return f
	case string:
		trim := strings.TrimSpace(strings.TrimPrefix(val, "$"))
		var out float64
		fmt.Sscanf(trim, "%f", &out)
		return out
	}
	return 0
}

func boolValue(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		switch strings.ToLower(strings.TrimSpace(val)) {
		case "true", "1", "yes", "y":
			return true
		}
	}
	return false
}
