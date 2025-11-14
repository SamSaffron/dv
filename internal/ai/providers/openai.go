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

type openAIHint struct {
	ContextTokens int
	InputCost     float64
	OutputCost    float64
}

var openAIModelHints = map[string]openAIHint{
	"gpt-4o":                 {ContextTokens: 128000, InputCost: 5, OutputCost: 15},
	"gpt-4o-mini":            {ContextTokens: 128000, InputCost: 0.15, OutputCost: 0.60},
	"gpt-4.1-mini":           {ContextTokens: 200000, InputCost: 1.5, OutputCost: 6},
	"gpt-4.1":                {ContextTokens: 200000, InputCost: 10, OutputCost: 30},
	"gpt-4.1-preview":        {ContextTokens: 128000, InputCost: 5, OutputCost: 15},
	"gpt-4o-realtime":        {ContextTokens: 128000, InputCost: 5, OutputCost: 15},
	"o1":                     {ContextTokens: 200000, InputCost: 15, OutputCost: 60},
	"o1-mini":                {ContextTokens: 200000, InputCost: 3, OutputCost: 12},
	"o1-preview":             {ContextTokens: 200000, InputCost: 15, OutputCost: 60},
	"o3-mini":                {ContextTokens: 200000, InputCost: 1.1, OutputCost: 4.4},
	"gpt-4-turbo":            {ContextTokens: 128000, InputCost: 10, OutputCost: 30},
	"gpt-4.0":                {ContextTokens: 8192, InputCost: 30, OutputCost: 60},
	"gpt-4.0-mini":           {ContextTokens: 32768, InputCost: 0.3, OutputCost: 1.2},
	"gpt-3.5-turbo":          {ContextTokens: 16385, InputCost: 0.6, OutputCost: 2.0},
	"gpt-3.5-turbo-16k":      {ContextTokens: 16385, InputCost: 1.2, OutputCost: 4.0},
	"text-embedding-3":       {ContextTokens: 8192, InputCost: 0.02, OutputCost: 0},
	"text-embedding-3-large": {ContextTokens: 8192, InputCost: 0.13, OutputCost: 0},
	"text-embedding-3-small": {ContextTokens: 8192, InputCost: 0.02, OutputCost: 0},
	"whisper-1":              {ContextTokens: 0, InputCost: 18, OutputCost: 0}, // $0.018/min => $18 per 1M tokens approx.
}

type openAIConnector struct{}

func (c *openAIConnector) id() string    { return "openai" }
func (c *openAIConnector) title() string { return "OpenAI" }
func (c *openAIConnector) envKeys() []string {
	return []string{"OPENAI_API_KEY"}
}
func (c *openAIConnector) hasCredentials(env map[string]string) bool {
	return firstEnv(env, c.envKeys()) != ""
}

func (c *openAIConnector) fetch(ctx context.Context, client *http.Client, env map[string]string) ([]ai.ProviderModel, time.Time, error) {
	apiKey := firstEnv(env, c.envKeys())
	if apiKey == "" {
		return nil, time.Time{}, errMissingAPIKey
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.openai.com/v1/models", nil)
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
		return nil, time.Time{}, unauthorizedErr("OpenAI")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, time.Time{}, fmt.Errorf("openai %s: %s", resp.Status, string(body))
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
		if id == "" || !isInterestingOpenAIModel(id) {
			continue
		}
		display := id
		if desc := stringValue(obj["description"]); desc != "" {
			display = desc
		}

		tags := []string{}
		if owner := stringValue(obj["owned_by"]); owner != "" {
			tags = append(tags, owner)
		}
		if strings.Contains(id, "omni") || strings.Contains(id, "vision") {
			tags = append(tags, "vision")
		}

		hint, _ := lookupOpenAIHint(id)
		if pricing, ok := obj["pricing"].(map[string]interface{}); ok {
			if prompt := getOpenAIPrice(pricing["prompt"]); prompt > 0 {
				hint.InputCost = prompt
			} else if input := getOpenAIPrice(pricing["input"]); input > 0 {
				hint.InputCost = input
			}
			if completion := getOpenAIPrice(pricing["completion"]); completion > 0 {
				hint.OutputCost = completion
			} else if output := getOpenAIPrice(pricing["output"]); output > 0 {
				hint.OutputCost = output
			}
		}

		models = append(models, ai.ProviderModel{
			ID:                id,
			DisplayName:       display,
			Provider:          "open_ai",
			Family:            stringValue(obj["owned_by"]),
			Endpoint:          resolveOpenAIEndpoint(id),
			Tokenizer:         "DiscourseAi::Tokenizer::OpenAiTokenizer",
			ContextTokens:     hint.ContextTokens,
			InputCost:         hint.InputCost,
			CachedInputCost:   0,
			OutputCost:        hint.OutputCost,
			SupportsVision:    strings.Contains(id, "vision") || strings.Contains(id, "omni"),
			SupportsReasoning: strings.HasPrefix(id, "o1") || strings.HasPrefix(id, "o3"),
			Description:       display,
			Tags:              tags,
			UpdatedAt:         now,
			Raw:               obj,
		})
	}
	return models, now, nil
}

func isInterestingOpenAIModel(id string) bool {
	lower := strings.ToLower(id)
	switch {
	case strings.HasPrefix(lower, "gpt"),
		strings.HasPrefix(lower, "o1"),
		strings.HasPrefix(lower, "o3"),
		strings.Contains(lower, "omni"),
		strings.Contains(lower, "gpt-4"),
		strings.Contains(lower, "gpt-5"),
		strings.Contains(lower, "text-embedding"),
		strings.Contains(lower, "text-embedding-3"),
		strings.Contains(lower, "text-embedding-ada"),
		strings.Contains(lower, "whisper"),
		strings.HasPrefix(lower, "tts"):
		return true
	default:
		return false
	}
}

func resolveOpenAIEndpoint(id string) string {
	lower := strings.ToLower(id)
	if strings.Contains(lower, "embedding") {
		return "https://api.openai.com/v1/embeddings"
	}
	if strings.Contains(lower, "tts") || strings.Contains(lower, "speech") {
		return "https://api.openai.com/v1/audio/speech"
	}
	return "https://api.openai.com/v1/responses"
}

func lookupOpenAIHint(id string) (openAIHint, bool) {
	lower := strings.ToLower(id)
	for key, hint := range openAIModelHints {
		if strings.Contains(lower, key) {
			return hint, true
		}
	}
	return openAIHint{}, false
}

func getOpenAIPrice(v interface{}) float64 {
	switch val := v.(type) {
	case map[string]interface{}:
		if unit := strings.ToLower(stringValue(val["unit"])); unit != "" {
			amount := floatValue(val["value"])
			switch unit {
			case "1m tokens", "one million tokens", "per million tokens":
				return amount
			case "1k tokens", "one thousand tokens":
				return amount * 1000
			case "token", "per token":
				return amount * 1_000_000
			}
		}
		for _, key := range []string{"usd_per_1m_tokens", "usd_per_million_tokens"} {
			if price, ok := val[key]; ok {
				return floatValue(price)
			}
		}
		if nested, ok := val["usd"]; ok {
			return getOpenAIPrice(nested)
		}
	case float64:
		return val
	case string:
		var parsed float64
		fmt.Sscanf(strings.TrimSpace(val), "%f", &parsed)
		return parsed
	}
	return 0
}
