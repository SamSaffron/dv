package resources

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"text/template"
)

var (
	//go:embed ai_tool_tool.yml.tmpl
	aiToolConfigTemplateBytes []byte
	//go:embed ai_tool_sync.rb.tmpl
	aiToolSyncScriptBytes []byte
	//go:embed ai_tool_test.rb.tmpl
	aiToolTestScriptBytes []byte

	aiToolConfigTemplate = template.Must(
		template.New("ai-tool-config").Funcs(template.FuncMap{
			"quote": yamlQuote,
		}).Parse(string(aiToolConfigTemplateBytes)),
	)
)

// AiToolParameterTemplateData captures a parameter row inside tool.yml.
type AiToolParameterTemplateData struct {
	Name        string
	Type        string
	Description string
	Required    bool
	Enum        []string
}

// AiToolConfigTemplateData describes the values rendered into tool.yml.
type AiToolConfigTemplateData struct {
	DisplayName           string
	Name                  string
	ToolName              string
	Summary               string
	Description           string
	Parameters            []AiToolParameterTemplateData
	RagChunkTokens        int
	RagChunkOverlapTokens int
}

// RenderAiToolConfig renders the tool.yml template with the supplied values.
func RenderAiToolConfig(data AiToolConfigTemplateData) (string, error) {
	var buf bytes.Buffer
	if err := aiToolConfigTemplate.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// AiToolSyncScript returns the Ruby script that upserts the tool into Discourse.
func AiToolSyncScript() string {
	return string(aiToolSyncScriptBytes)
}

// AiToolTestScript returns the Ruby script that runs the tool locally inside Discourse.
func AiToolTestScript() string {
	return string(aiToolTestScriptBytes)
}

func yamlQuote(value string) string {
	b, _ := json.Marshal(value)
	return string(b)
}
