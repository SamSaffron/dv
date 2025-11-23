package discourse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dv/internal/ai"
	"dv/internal/docker"
)

// DecodeError surfaces the raw payload when JSON decoding fails.
type DecodeError struct {
	Payload string
	Err     error
}

func (e DecodeError) Error() string {
	if e.Err != nil {
		// Include a preview of the payload (first 500 chars) to help debug
		preview := e.Payload
		if len(preview) > 500 {
			preview = preview[:500] + "...(truncated)"
		}
		return fmt.Sprintf("%s\n\nReceived output:\n%s", e.Err.Error(), preview)
	}
	return "decode error"
}

func (e DecodeError) Unwrap() error {
	return e.Err
}

// Client wraps helper methods for interacting with Discourse via rails runner.
type Client struct {
	ContainerName string
	Workdir       string
	Verbose       bool
}

// NewClient builds a Client bound to the given container/workdir.
func NewClient(container, workdir string, verbose bool) *Client {
	return &Client{ContainerName: container, Workdir: workdir, Verbose: verbose}
}

// FetchState returns all configured LLMs plus metadata used by the TUI.
func (c *Client) FetchState(ctx context.Context) (ai.LLMState, error) {
	var state ai.LLMState
	out, err := c.runRails(fetchScript)
	if err != nil {
		return state, err
	}

	if c.Verbose {
		fmt.Fprintf(os.Stderr, "=== RAW RAILS OUTPUT (%d bytes) ===\n", len(out))
		fmt.Fprintln(os.Stderr, out)
		fmt.Fprintln(os.Stderr, "=== END RAW OUTPUT ===")
	}

	// Extract JSON from output - skip any warnings/noise before the JSON
	jsonOutput, err := c.extractJSON(out)
	if err != nil {
		return state, err
	}

	var payload struct {
		DefaultLLMID int64          `json:"default_llm_id"`
		Models       []ai.LLMModel  `json:"ai_llms"`
		Meta         ai.LLMMetadata `json:"meta"`
	}
	if err := json.Unmarshal([]byte(jsonOutput), &payload); err != nil {
		return state, DecodeError{
			Payload: jsonOutput,
			Err:     fmt.Errorf("decode LLM payload: %w", err),
		}
	}
	state.DefaultID = payload.DefaultLLMID
	state.Models = payload.Models
	state.Meta = payload.Meta
	return state, nil
}

// SetDefaultLLM updates SiteSetting.ai_default_llm_model.
func (c *Client) SetDefaultLLM(ctx context.Context, id int64) error {
	script := fmt.Sprintf(`
SiteSetting.ai_default_llm_model = %d
STDOUT.sync = true
puts JSON.fast_generate({ default_llm_id: SiteSetting.ai_default_llm_model })
`, id)
	_, err := c.runRails(script)
	return err
}

// CreateModelInput captures the core attributes required to define a new LLM.
type CreateModelInput struct {
	DisplayName     string
	Name            string
	Provider        string
	Tokenizer       string
	URL             string
	APIKey          string
	MaxPromptTokens int
	MaxOutputTokens int
	InputCost       float64
	CachedInputCost float64
	OutputCost      float64
	EnabledChatBot  bool
	VisionEnabled   bool
	ProviderParams  map[string]interface{}
	SetAsDefault    bool
	ExistingID      int64
}

// CreateModel provisions a new LLMModel record inside Discourse.
func (c *Client) CreateModel(ctx context.Context, input CreateModelInput) (int64, error) {
	attrs := buildLLMAttributes(input, true)
	payload := map[string]interface{}{
		"attributes":  attrs,
		"set_default": input.SetAsDefault,
	}

	// Generate a unique filename
	tmpFile, err := os.CreateTemp("", "dv-llm-create-*.json")
	if err != nil {
		return 0, err
	}
	tmpFile.Close()
	os.Remove(tmpFile.Name()) // Remove temp file, we'll write directly to container

	containerPath := fmt.Sprintf("/tmp/%s", filepath.Base(tmpFile.Name()))
	if err := c.writeJSONToContainer(payload, containerPath); err != nil {
		return 0, fmt.Errorf("write payload: %w", err)
	}
	defer docker.ExecOutput(c.ContainerName, c.Workdir, []string{"bash", "-lc", "rm -f " + shellQuote(containerPath)})

	createScript := fmt.Sprintf(createScriptTemplate, shellQuote(containerPath))
	out, err := c.runRails(createScript)
	if err != nil {
		return 0, err
	}
	jsonOutput, err := c.extractJSON(out)
	if err != nil {
		return 0, err
	}
	var response struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(jsonOutput), &response); err != nil {
		return 0, fmt.Errorf("decode create response: %w", err)
	}
	return response.ID, nil
}

// runRails executes the provided Ruby code via rails runner inside the container.
func (c *Client) runRails(ruby string) (string, error) {
	cmd := fmt.Sprintf("RAILS_ENV=development bundle exec rails runner - <<'RUBY'\n%s\nRUBY", ruby)
	out, err := docker.ExecOutput(c.ContainerName, c.Workdir, []string{"bash", "-lc", cmd})
	if err != nil {
		if strings.TrimSpace(out) != "" {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(out))
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// extractJSON finds and returns the JSON portion from Rails output, skipping any warnings/noise.
func (c *Client) extractJSON(out string) (string, error) {
	jsonStart := strings.Index(out, "{")
	if jsonStart == -1 {
		return "", DecodeError{
			Payload: out,
			Err:     fmt.Errorf("no JSON object found in output"),
		}
	}

	if c.Verbose && jsonStart > 0 {
		fmt.Fprintf(os.Stderr, "=== SKIPPED %d bytes of non-JSON output ===\n", jsonStart)
		fmt.Fprintf(os.Stderr, "%s\n", out[:jsonStart])
	}

	return out[jsonStart:], nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

const fetchScript = `
require "json"

ActiveRecord::Base.logger = nil
Rails.logger.level = 4

default_llm_value = SiteSetting.ai_default_llm_model
default_llm_id = if default_llm_value.present?
  default_llm_value.to_i
else
  0
end

llms = LlmModel
  .includes(:llm_quotas, :llm_credit_allocation, :llm_feature_credit_costs)
  .order(:display_name)
usage = DiscourseAi::Configuration::LlmEnumerator.global_usage

payload = {
  default_llm_id: default_llm_id,
  ai_llms: llms.map do |llm|
    {
      id: llm.id,
      display_name: llm.display_name,
      name: llm.name,
      provider: llm.provider,
      tokenizer: llm.tokenizer,
      url: llm.url,
      max_prompt_tokens: llm.max_prompt_tokens,
      max_output_tokens: llm.max_output_tokens,
      input_cost: llm.input_cost,
      cached_input_cost: llm.cached_input_cost,
      output_cost: llm.output_cost,
      enabled_chat_bot: llm.enabled_chat_bot,
      vision_enabled: llm.vision_enabled,
      provider_params: llm.provider_params || {},
      used_by: usage[llm.id] || [],
      llm_quotas: llm.llm_quotas.map do |quota|
        {
          group_id: quota.group_id,
          max_tokens: quota.max_tokens,
          max_usages: quota.max_usages,
          duration_seconds: quota.duration_seconds,
        }
      end,
      llm_credit_allocation: llm.llm_credit_allocation&.attributes,
      llm_feature_credit_costs: llm.llm_feature_credit_costs.map(&:attributes),
    }
  end,
  meta: {
    provider_params: LlmModel.provider_params,
    presets: DiscourseAi::Completions::Llm.presets,
    providers: DiscourseAi::Completions::Llm.provider_names,
    tokenizers: DiscourseAi::Completions::Llm.tokenizer_names.map { |tn| { id: tn, name: tn.split("::").last } },
  },
}

STDOUT.sync = true
puts JSON.fast_generate(payload)
`

const createScriptTemplate = `
require "json"
ActiveRecord::Base.logger = nil
Rails.logger.level = 4

payload_path = %s
data = JSON.parse(File.read(payload_path))
attrs = data["attributes"] || {}
llm = LlmModel.new(attrs)

if quotas = data["llm_quotas"]
  quotas.each do |quota|
    llm.llm_quotas.build(quota)
  end
end

llm.save!
llm.toggle_companion_user

if data["set_default"]
  SiteSetting.ai_default_llm_model = llm.id
end

STDOUT.sync = true
puts JSON.fast_generate({ id: llm.id })
`

// DeleteModel removes an existing LLMModel, mirroring controller safeguards.
func (c *Client) DeleteModel(ctx context.Context, id int64) error {
	script := fmt.Sprintf(deleteScriptTemplate, id)
	_, err := c.runRails(script)
	return err
}

// UpdateModel persists edits to an existing LLMModel.
func (c *Client) UpdateModel(ctx context.Context, id int64, input CreateModelInput) error {
	attrs := buildLLMAttributes(input, true)
	payload := map[string]interface{}{
		"id":          id,
		"attributes":  attrs,
		"set_default": input.SetAsDefault,
	}

	// Generate a unique filename
	tmpFile, err := os.CreateTemp("", "dv-llm-update-*.json")
	if err != nil {
		return err
	}
	tmpFile.Close()
	os.Remove(tmpFile.Name()) // Remove temp file, we'll write directly to container

	containerPath := fmt.Sprintf("/tmp/%s", filepath.Base(tmpFile.Name()))
	if err := c.writeJSONToContainer(payload, containerPath); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	defer docker.ExecOutput(c.ContainerName, c.Workdir, []string{"bash", "-lc", "rm -f " + shellQuote(containerPath)})

	updateScript := fmt.Sprintf(updateScriptTemplate, shellQuote(containerPath))
	_, err = c.runRails(updateScript)
	return err
}

// TestModel validates/test-runs an LLM definition via Discourse's validator.
func (c *Client) TestModel(ctx context.Context, input CreateModelInput) error {
	attrs := buildLLMAttributes(input, true)
	payload := map[string]interface{}{
		"attributes": attrs,
	}
	if input.ExistingID != 0 {
		payload["id"] = input.ExistingID
	}

	if strings.TrimSpace(input.APIKey) == "" && input.ExistingID == 0 {
		return fmt.Errorf("API key is required to test an LLM")
	}

	// Generate a unique filename
	tmpFile, err := os.CreateTemp("", "dv-llm-test-*.json")
	if err != nil {
		return err
	}
	tmpFile.Close()
	os.Remove(tmpFile.Name()) // Remove temp file, we'll write directly to container

	containerPath := fmt.Sprintf("/tmp/%s", filepath.Base(tmpFile.Name()))
	if err := c.writeJSONToContainer(payload, containerPath); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	defer docker.ExecOutput(c.ContainerName, c.Workdir, []string{"bash", "-lc", "rm -f " + shellQuote(containerPath)})

	testScript := fmt.Sprintf(testScriptTemplate, shellQuote(containerPath))
	_, err = c.runRails(testScript)
	return err
}

const deleteScriptTemplate = `
require "json"
ActiveRecord::Base.logger = nil
Rails.logger.level = 4

llm = LlmModel.find_by(id: %d)
raise "LLM model not found" if llm.nil?
if llm.seeded?
  raise "Cannot delete built-in LLM models"
end
in_use = DiscourseAi::Configuration::LlmValidator.new.is_using(llm)
if in_use.present?
  raise "Model is still in use by: #{in_use.join(", ")}"
end
llm.enabled_chat_bot = false
llm.toggle_companion_user
llm.destroy!
STDOUT.sync = true
puts JSON.fast_generate({ deleted: true, id: llm.id })
`

const updateScriptTemplate = `
require "json"
ActiveRecord::Base.logger = nil
Rails.logger.level = 4

payload_path = %s
data = JSON.parse(File.read(payload_path))
llm = LlmModel.find_by(id: data["id"])
raise "LLM model not found" if llm.nil?

attrs = data["attributes"] || {}
llm.update!(attrs)
llm.toggle_companion_user

if data["set_default"]
  SiteSetting.ai_default_llm_model = llm.id
end

STDOUT.sync = true
puts JSON.fast_generate({ id: llm.id })
`

const testScriptTemplate = `
require "json"
ActiveRecord::Base.logger = nil
Rails.logger.level = 4

payload_path = %s
data = JSON.parse(File.read(payload_path))
attrs = data["attributes"] || {}

if data["id"]
  llm = LlmModel.find_by(id: data["id"])
  raise "LLM model not found" if llm.nil?
  attrs = attrs.transform_keys(&:to_sym)
  api_key = attrs.delete(:api_key)
  llm.assign_attributes(attrs)
  llm.api_key = api_key if api_key.present?
else
  llm = LlmModel.new(attrs)
end

if llm.valid?
  DiscourseAi::Configuration::LlmValidator.new.run_test(llm)
else
  raise llm.errors.full_messages.join(", ")
end

STDOUT.sync = true
puts JSON.fast_generate({ success: true })
`

// EnableFeatures flips Discourse AI feature site settings to true for easier testing.
func (c *Client) EnableFeatures(ctx context.Context, settings []string, env map[string]string) error {
	if len(settings) == 0 {
		return nil
	}
	var quoted []string
	for _, name := range settings {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}
	if len(quoted) == 0 {
		return nil
	}
	script := fmt.Sprintf(enableFeaturesTemplate, strings.Join(quoted, ", "))
	_, err := c.runRails(script)
	if err != nil {
		return err
	}

	// Set ai_bot_github_access_token from GH_TOKEN if available
	if ghToken := strings.TrimSpace(env["GH_TOKEN"]); ghToken != "" {
		setTokenScript := fmt.Sprintf(`
SiteSetting.ai_bot_github_access_token = %q
STDOUT.sync = true
puts JSON.fast_generate({ github_token_set: true })
`, ghToken)
		_, err = c.runRails(setTokenScript)
		return err
	}
	return nil
}

const enableFeaturesTemplate = `
require "json"
ActiveRecord::Base.logger = nil
Rails.logger.level = 4

SiteSetting.discourse_ai_enabled = true
[
%s
].each do |setting|
  writer = "#{setting}="
  next unless SiteSetting.respond_to?(writer)
  begin
    SiteSetting.public_send(writer, true)
  rescue => e
    STDERR.puts("dv-ai: failed to enable #{setting}: #{e.message}")
  end
end

staff_group = Group[:staff]
if staff_group
  SiteSetting.ai_bot_debugging_allowed_groups = staff_group.id
end

STDOUT.sync = true
puts JSON.fast_generate({ enabled: true })
`

func buildLLMAttributes(input CreateModelInput, includeAPIKey bool) map[string]interface{} {
	attrs := map[string]interface{}{
		"display_name":      strings.TrimSpace(input.DisplayName),
		"name":              strings.TrimSpace(input.Name),
		"provider":          strings.TrimSpace(input.Provider),
		"tokenizer":         strings.TrimSpace(input.Tokenizer),
		"url":               strings.TrimSpace(input.URL),
		"max_prompt_tokens": input.MaxPromptTokens,
		"max_output_tokens": input.MaxOutputTokens,
		"input_cost":        input.InputCost,
		"cached_input_cost": input.CachedInputCost,
		"output_cost":       input.OutputCost,
		"enabled_chat_bot":  input.EnabledChatBot,
		"vision_enabled":    input.VisionEnabled,
	}
	if includeAPIKey {
		if apiKey := strings.TrimSpace(input.APIKey); apiKey != "" {
			attrs["api_key"] = apiKey
		}
	}
	if input.ProviderParams != nil {
		attrs["provider_params"] = input.ProviderParams
	}
	return attrs
}

// writeJSONToContainer writes JSON data directly to a file in the container as the discourse user,
// avoiding permission issues that occur with docker cp.
func (c *Client) writeJSONToContainer(data interface{}, containerPath string) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}

	// Write directly to container using docker exec with stdin, creating file as discourse user
	args := []string{"exec", "-i", "--user", "discourse", "-w", "/", c.ContainerName, "bash", "-c", fmt.Sprintf("cat > %s", shellQuote(containerPath))}
	cmd := exec.Command("docker", args...)
	cmd.Stdin = bytes.NewReader(jsonData)
	if c.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	}
	return cmd.Run()
}
