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
	ContextTokens   int
	InputCost       float64
	CachedInputCost float64
	OutputCost      float64
}

// OpenAI pricing as of 2025-11 from https://platform.openai.com/docs/pricing
// Prices are in USD per 1M tokens
var openAIModelHints = map[string]openAIHint{
	// GPT-5 models
	"gpt-5.1":            {ContextTokens: 200000, InputCost: 1.25, CachedInputCost: 0.125, OutputCost: 10.0},
	"gpt-5":              {ContextTokens: 200000, InputCost: 1.25, CachedInputCost: 0.125, OutputCost: 10.0},
	"gpt-5-mini":         {ContextTokens: 200000, InputCost: 0.25, CachedInputCost: 0.025, OutputCost: 2.0},
	"gpt-5-nano":         {ContextTokens: 200000, InputCost: 0.05, CachedInputCost: 0.005, OutputCost: 0.40},
	"gpt-5.1-chat":       {ContextTokens: 200000, InputCost: 1.25, CachedInputCost: 0.125, OutputCost: 10.0},
	"gpt-5-chat":         {ContextTokens: 200000, InputCost: 1.25, CachedInputCost: 0.125, OutputCost: 10.0},
	"gpt-5.1-codex":      {ContextTokens: 200000, InputCost: 1.25, CachedInputCost: 0.125, OutputCost: 10.0},
	"gpt-5-codex":        {ContextTokens: 200000, InputCost: 1.25, CachedInputCost: 0.125, OutputCost: 10.0},
	"gpt-5-pro":          {ContextTokens: 200000, InputCost: 15.0, OutputCost: 120.0},
	"gpt-5.1-codex-mini": {ContextTokens: 200000, InputCost: 0.25, CachedInputCost: 0.025, OutputCost: 2.0},
	"gpt-5-search-api":   {ContextTokens: 200000, InputCost: 1.25, CachedInputCost: 0.125, OutputCost: 10.0},

	// GPT-4.1 models
	"gpt-4.1":      {ContextTokens: 200000, InputCost: 2.0, CachedInputCost: 0.50, OutputCost: 8.0},
	"gpt-4.1-mini": {ContextTokens: 200000, InputCost: 0.40, CachedInputCost: 0.10, OutputCost: 1.60},
	"gpt-4.1-nano": {ContextTokens: 200000, InputCost: 0.10, CachedInputCost: 0.025, OutputCost: 0.40},

	// GPT-4o models
	"gpt-4o":                       {ContextTokens: 128000, InputCost: 2.50, CachedInputCost: 1.25, OutputCost: 10.0},
	"gpt-4o-2024-05-13":            {ContextTokens: 128000, InputCost: 5.0, OutputCost: 15.0},
	"gpt-4o-mini":                  {ContextTokens: 128000, InputCost: 0.15, CachedInputCost: 0.075, OutputCost: 0.60},
	"gpt-4o-mini-search-preview":   {ContextTokens: 128000, InputCost: 0.15, OutputCost: 0.60},
	"gpt-4o-search-preview":        {ContextTokens: 128000, InputCost: 2.50, OutputCost: 10.0},
	"gpt-4o-audio-preview":         {ContextTokens: 128000, InputCost: 2.50, OutputCost: 10.0},
	"gpt-4o-mini-audio-preview":    {ContextTokens: 128000, InputCost: 0.15, OutputCost: 0.60},
	"gpt-4o-realtime-preview":      {ContextTokens: 128000, InputCost: 5.0, CachedInputCost: 2.50, OutputCost: 20.0},
	"gpt-4o-mini-realtime-preview": {ContextTokens: 128000, InputCost: 0.60, CachedInputCost: 0.30, OutputCost: 2.40},

	// Realtime models
	"gpt-realtime":      {ContextTokens: 128000, InputCost: 4.0, CachedInputCost: 0.40, OutputCost: 16.0},
	"gpt-realtime-mini": {ContextTokens: 128000, InputCost: 0.60, CachedInputCost: 0.06, OutputCost: 2.40},

	// Audio models
	"gpt-audio":      {ContextTokens: 128000, InputCost: 2.50, OutputCost: 10.0},
	"gpt-audio-mini": {ContextTokens: 128000, InputCost: 0.60, OutputCost: 2.40},

	// o-series reasoning models
	"o1":                    {ContextTokens: 200000, InputCost: 15.0, CachedInputCost: 7.50, OutputCost: 60.0},
	"o1-mini":               {ContextTokens: 128000, InputCost: 1.10, CachedInputCost: 0.55, OutputCost: 4.40},
	"o1-pro":                {ContextTokens: 200000, InputCost: 150.0, OutputCost: 600.0},
	"o3":                    {ContextTokens: 200000, InputCost: 2.0, CachedInputCost: 0.50, OutputCost: 8.0},
	"o3-mini":               {ContextTokens: 200000, InputCost: 1.10, CachedInputCost: 0.55, OutputCost: 4.40},
	"o3-pro":                {ContextTokens: 200000, InputCost: 20.0, OutputCost: 80.0},
	"o3-deep-research":      {ContextTokens: 200000, InputCost: 10.0, CachedInputCost: 2.50, OutputCost: 40.0},
	"o4-mini":               {ContextTokens: 200000, InputCost: 1.10, CachedInputCost: 0.275, OutputCost: 4.40},
	"o4-mini-deep-research": {ContextTokens: 200000, InputCost: 2.0, CachedInputCost: 0.50, OutputCost: 8.0},

	// Other models
	"computer-use-preview": {ContextTokens: 128000, InputCost: 3.0, OutputCost: 12.0},
	"codex-mini":           {ContextTokens: 200000, InputCost: 1.50, CachedInputCost: 0.375, OutputCost: 6.0},

	// Image generation models
	"gpt-image-1":      {ContextTokens: 0, InputCost: 5.0, CachedInputCost: 1.25, OutputCost: 0},
	"gpt-image-1-mini": {ContextTokens: 0, InputCost: 2.0, CachedInputCost: 0.20, OutputCost: 0},

	// Legacy GPT-4 models
	"gpt-4-turbo": {ContextTokens: 128000, InputCost: 10.0, CachedInputCost: 5.0, OutputCost: 30.0},
	"gpt-4":       {ContextTokens: 8192, InputCost: 30.0, OutputCost: 60.0},

	// GPT-3.5 Turbo (legacy)
	"gpt-3.5-turbo": {ContextTokens: 16385, InputCost: 0.50, OutputCost: 1.50},

	// Embeddings
	"text-embedding-3-large": {ContextTokens: 8192, InputCost: 0.13, OutputCost: 0},
	"text-embedding-3-small": {ContextTokens: 8192, InputCost: 0.02, OutputCost: 0},
	"text-embedding-ada":     {ContextTokens: 8192, InputCost: 0.10, OutputCost: 0},

	// Audio (legacy)
	"whisper-1": {ContextTokens: 0, InputCost: 6.0, OutputCost: 0},  // $0.006/minute
	"tts-1":     {ContextTokens: 0, InputCost: 15.0, OutputCost: 0}, // $0.015 per 1K chars
	"tts-1-hd":  {ContextTokens: 0, InputCost: 30.0, OutputCost: 0}, // $0.030 per 1K chars
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
			CachedInputCost:   hint.CachedInputCost,
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
		strings.HasPrefix(lower, "o4"),
		strings.HasPrefix(lower, "chatgpt"),
		strings.Contains(lower, "omni"),
		strings.Contains(lower, "realtime"),
		strings.Contains(lower, "audio"),
		strings.Contains(lower, "codex"),
		strings.Contains(lower, "computer-use"),
		strings.Contains(lower, "text-embedding"),
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
