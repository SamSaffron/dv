package localproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"dv/internal/config"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func newClient(cfg config.LocalProxyConfig) *Client {
	timeout := 4 * time.Second
	return &Client{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", cfg.APIPort),
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) Health() error {
	resp, err := c.http.Get(c.baseURL + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("health check failed: %s", resp.Status)
}

func (c *Client) Register(host string, target string) error {
	payload := map[string]string{
		"host":   host,
		"target": target,
	}
	body, _ := json.Marshal(payload)
	resp, err := c.http.Post(c.baseURL+"/api/routes", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		return nil
	}
	return fmt.Errorf("proxy registration failed: %s", readErrorBody(resp.Body))
}

func (c *Client) Remove(host string) error {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+"/api/routes/"+host, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("proxy remove failed: %s", readErrorBody(resp.Body))
}

func readErrorBody(r io.Reader) string {
	if r == nil {
		return "no response body"
	}
	data, err := io.ReadAll(io.LimitReader(r, 1024))
	if err != nil {
		return err.Error()
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return "empty response"
	}
	return string(bytes.TrimSpace(data))
}
