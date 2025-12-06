package discourse

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// SiteSetting represents a Discourse site setting
type SiteSetting struct {
	Setting     string      `json:"setting"`
	Value       interface{} `json:"value"`
	Default     interface{} `json:"default"`
	Description string      `json:"description"`
	Type        string      `json:"type"`
}

// SiteSettingsResponse is the API response for site settings
type SiteSettingsResponse struct {
	SiteSettings []SiteSetting `json:"site_settings"`
}

// GetSiteSetting retrieves a single site setting by name
func (c *Client) GetSiteSetting(name string) (interface{}, error) {
	path := fmt.Sprintf("/admin/site_settings.json?filter=%s", url.QueryEscape(name))
	resp, body, err := c.doRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("get setting %s: status %d: %s", name, resp.StatusCode, string(body))
	}

	var result SiteSettingsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode settings: %w", err)
	}

	for _, s := range result.SiteSettings {
		if s.Setting == name {
			return s.Value, nil
		}
	}

	return nil, fmt.Errorf("setting %s not found", name)
}

// SetSiteSetting updates a site setting value
func (c *Client) SetSiteSetting(name string, value interface{}) error {
	path := fmt.Sprintf("/admin/site_settings/%s.json", url.PathEscape(name))
	payload := map[string]interface{}{
		name: value,
	}

	resp, body, err := c.doRequest("PUT", path, payload)
	if err != nil {
		return err
	}

	// Accept 200 OK and 204 No Content as success
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return fmt.Errorf("set setting %s: status %d: %s", name, resp.StatusCode, string(body))
	}

	return nil
}

// EnableSiteSettings enables multiple boolean site settings.
// It always enables discourse_ai_enabled first as a prerequisite.
func (c *Client) EnableSiteSettings(names []string) error {
	// Always enable the main AI plugin first
	if err := c.SetSiteSetting("discourse_ai_enabled", true); err != nil {
		c.verboseLog("Warning: failed to enable discourse_ai_enabled: %v", err)
	}

	for _, name := range names {
		if err := c.SetSiteSetting(name, true); err != nil {
			c.verboseLog("Warning: failed to enable %s: %v", name, err)
			// Continue with other settings
		}
	}
	return nil
}

// GetSiteSettingBool retrieves a boolean site setting
func (c *Client) GetSiteSettingBool(name string) (bool, error) {
	val, err := c.GetSiteSetting(name)
	if err != nil {
		return false, err
	}

	switch v := val.(type) {
	case bool:
		return v, nil
	case string:
		return v == "true" || v == "t" || v == "1", nil
	default:
		return false, fmt.Errorf("setting %s is not a boolean: %T", name, val)
	}
}

// GetSiteSettingString retrieves a string site setting
func (c *Client) GetSiteSettingString(name string) (string, error) {
	val, err := c.GetSiteSetting(name)
	if err != nil {
		return "", err
	}

	switch v := val.(type) {
	case string:
		return v, nil
	case float64:
		return fmt.Sprintf("%v", v), nil
	default:
		return fmt.Sprintf("%v", val), nil
	}
}
