package resources

import (
	"bytes"
	_ "embed"
	"text/template"
)

var (
	//go:embed theme_agents.md.tmpl
	themeAgentTemplateBytes []byte
	themeAgentTemplate      = template.Must(template.New("theme-agent").Parse(string(themeAgentTemplateBytes)))
)

// ThemeAgentData parameterizes the AGENTS.md template exposed to dv config theme.
type ThemeAgentData struct {
	ThemeName              string
	ThemePath              string
	ContainerName          string
	ContainerDiscoursePath string
	HostDiscoursePath      string
	RepositoryURL          string
	ServiceName            string
	IsComponent            bool
}

// RenderThemeAgent fills the embedded AGENTS.md template with workspace guidance.
func RenderThemeAgent(data ThemeAgentData) (string, error) {
	var buf bytes.Buffer
	if err := themeAgentTemplate.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
