package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"dv/internal/ai"
)

type geminiConnector struct{}

func (c *geminiConnector) id() string    { return "gemini" }
func (c *geminiConnector) title() string { return "Google Gemini" }
func (c *geminiConnector) envKeys() []string {
	return []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}
}
func (c *geminiConnector) hasCredentials(env map[string]string) bool {
	return firstEnv(env, c.envKeys()) != ""
}

func (c *geminiConnector) fetch(ctx context.Context, client *http.Client, env map[string]string) ([]ai.ProviderModel, time.Time, error) {
	apiKey := firstEnv(env, c.envKeys())
	if apiKey == "" {
		return nil, time.Time{}, errMissingAPIKey
	}

	const baseURL = "https://generativelanguage.googleapis.com/v1beta/models"
	fetchedAt := time.Now()

	modelsByID := map[string]ai.ProviderModel{}
	pageToken := ""
	for {
		reqURL, err := url.Parse(baseURL)
		if err != nil {
			return nil, time.Time{}, err
		}
		query := reqURL.Query()
		query.Set("pageSize", "1000")
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		reqURL.RawQuery = query.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
		if err != nil {
			return nil, time.Time{}, err
		}
		req.Header.Set("x-goog-api-key", apiKey)
		req.Header.Set("User-Agent", "dv/ai-config")

		resp, err := client.Do(req)
		if err != nil {
			return nil, time.Time{}, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		resp.Body.Close()
		if readErr != nil {
			return nil, time.Time{}, readErr
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, time.Time{}, unauthorizedErr("Gemini")
		}
		if resp.StatusCode >= 400 {
			return nil, time.Time{}, fmt.Errorf("gemini %s: %s", resp.Status, string(body))
		}

		var root struct {
			Models        []json.RawMessage `json:"models"`
			NextPageToken string            `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &root); err != nil {
			return nil, time.Time{}, err
		}

		for _, raw := range root.Models {
			var obj map[string]interface{}
			if err := json.Unmarshal(raw, &obj); err != nil {
				continue
			}

			id := strings.TrimPrefix(strings.TrimSpace(stringValue(obj["baseModelId"])), "models/")
			if id == "" {
				id = strings.TrimPrefix(strings.TrimSpace(stringValue(obj["name"])), "models/")
			}
			if id == "" {
				continue
			}
			if !strings.HasPrefix(strings.ToLower(id), "gemini-") {
				continue
			}
			if !supportsGeminiMethod(obj, "generateContent") {
				continue
			}

			displayName := strings.TrimSpace(stringValue(obj["displayName"]))
			if displayName == "" {
				displayName = id
			}
			desc := strings.TrimSpace(stringValue(obj["description"]))

			inputTokenLimit := int(floatValue(firstExistingKey(obj, "inputTokenLimit", "input_token_limit")))
			outputTokenLimit := int(floatValue(firstExistingKey(obj, "outputTokenLimit", "output_token_limit")))

			rawObj := obj
			rawObj["dv_pricing_unknown"] = true

			model := ai.ProviderModel{
				ID:              id,
				DisplayName:     displayName,
				Provider:        "google",
				Family:          "gemini",
				Endpoint:        fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s", id),
				Tokenizer:       "DiscourseAi::Tokenizer::GeminiTokenizer",
				ContextTokens:   inputTokenLimit,
				SupportsVision:  true,
				Description:     desc,
				Tags:            []string{"google", "gemini"},
				UpdatedAt:       fetchedAt,
				Raw:             rawObj,
				CachedInputCost: 0,
				InputCost:       0,
				OutputCost:      0,
			}

			if outputTokenLimit > 0 {
				model.Raw["outputTokenLimit"] = outputTokenLimit
			}

			if existing, ok := modelsByID[id]; !ok || preferGeminiModel(existing, model) {
				modelsByID[id] = model
			}
		}

		if strings.TrimSpace(root.NextPageToken) == "" {
			break
		}
		pageToken = root.NextPageToken
	}

	models := make([]ai.ProviderModel, 0, len(modelsByID))
	for _, model := range modelsByID {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool {
		aiName := strings.ToLower(models[i].DisplayName)
		ajName := strings.ToLower(models[j].DisplayName)
		if aiName == ajName {
			return models[i].ID < models[j].ID
		}
		return aiName < ajName
	})

	return models, fetchedAt, nil
}

func supportsGeminiMethod(model map[string]interface{}, method string) bool {
	for _, key := range []string{"supportedGenerationMethods", "supported_generation_methods"} {
		if rawMethods, ok := model[key].([]interface{}); ok {
			for _, m := range rawMethods {
				if strings.EqualFold(strings.TrimSpace(stringValue(m)), method) {
					return true
				}
			}
		}
	}
	return false
}

func preferGeminiModel(existing, candidate ai.ProviderModel) bool {
	existingName := strings.TrimSpace(stringValue(existing.Raw["name"]))
	candidateName := strings.TrimSpace(stringValue(candidate.Raw["name"]))
	canonical := "models/" + candidate.ID

	if candidateName == canonical && existingName != canonical {
		return true
	}
	if existingName == canonical && candidateName != canonical {
		return false
	}
	if (existing.DisplayName == "" || existing.DisplayName == existing.ID) && candidate.DisplayName != "" && candidate.DisplayName != candidate.ID {
		return true
	}
	if candidate.ContextTokens > existing.ContextTokens {
		return true
	}

	existingOut := int(floatValue(existing.Raw["outputTokenLimit"]))
	candidateOut := int(floatValue(candidate.Raw["outputTokenLimit"]))
	return existingOut == 0 && candidateOut > 0
}

func firstExistingKey(m map[string]interface{}, keys ...string) interface{} {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return nil
}
