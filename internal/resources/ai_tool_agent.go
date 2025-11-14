package resources

import (
	"bytes"
	_ "embed"
	"text/template"
)

var (
	//go:embed ai_tool_agents.md.tmpl
	aiToolAgentTemplateBytes []byte
	aiToolAgentTemplate      = template.Must(template.New("ai-tool-agent").Parse(string(aiToolAgentTemplateBytes)))
)

// AiToolParameterSummary captures a brief overview of a parameter for the AGENTS.md guide.
type AiToolParameterSummary struct {
	Name        string
	Type        string
	Required    bool
	Description string
}

// AiToolAgentData parameterizes the AGENTS.md template for dv config ai-tool.
type AiToolAgentData struct {
	ToolDisplayName   string
	ToolName          string
	WorkspacePath     string
	ConfigPath        string
	ScriptPath        string
	TestPayloadPath   string
	ContainerName     string
	DiscourseRoot     string
	PresetName        string
	PresetDescription string
	ParameterSummary  []AiToolParameterSummary
}

// RenderAiToolAgent fills the embedded AGENTS.md template with workspace guidance.
func RenderAiToolAgent(data AiToolAgentData) (string, error) {
	var buf bytes.Buffer
	if err := aiToolAgentTemplate.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
