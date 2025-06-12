package templates

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed *.tpl.md
var templateFS embed.FS

// TemplateData holds the data for template rendering
type TemplateData struct {
	TaskContent    string                 `json:"task_content"`
	Context        string                 `json:"context"`
	Plan           string                 `json:"plan,omitempty"`
	ToolResults    string                 `json:"tool_results,omitempty"`
	Implementation string                 `json:"implementation,omitempty"`
	TestResults    string                 `json:"test_results,omitempty"`
	WorkDir        string                 `json:"work_dir,omitempty"`
	Extra          map[string]interface{} `json:"extra,omitempty"`
}

// StateTemplate represents a workflow state template
type StateTemplate string

const (
	// Coding agent templates
	PlanningTemplate       StateTemplate = "planning.tpl.md"
	ToolInvocationTemplate StateTemplate = "tool_invocation.tpl.md"
	CodingTemplate         StateTemplate = "coding.tpl.md"
	TestingTemplate        StateTemplate = "testing.tpl.md"
	ApprovalTemplate       StateTemplate = "approval.tpl.md"

	// Architect agent templates
	SpecAnalysisTemplate    StateTemplate = "spec_analysis.tpl.md"
	StoryGenerationTemplate StateTemplate = "story_generation.tpl.md"
	TechnicalQATemplate     StateTemplate = "technical_qa.tpl.md"
	CodeReviewTemplate      StateTemplate = "code_review.tpl.md"
)

// Renderer handles template rendering for workflow states
type Renderer struct {
	templates map[StateTemplate]*template.Template
}

// NewRenderer creates a new template renderer
func NewRenderer() (*Renderer, error) {
	r := &Renderer{
		templates: make(map[StateTemplate]*template.Template),
	}

	// Load all templates
	templateNames := []StateTemplate{
		// Coding agent templates
		PlanningTemplate,
		ToolInvocationTemplate,
		CodingTemplate,
		TestingTemplate,
		ApprovalTemplate,
		// Architect agent templates
		SpecAnalysisTemplate,
		StoryGenerationTemplate,
		TechnicalQATemplate,
		CodeReviewTemplate,
	}

	for _, name := range templateNames {
		content, err := templateFS.ReadFile(string(name))
		if err != nil {
			return nil, fmt.Errorf("failed to read template %s: %w", name, err)
		}

		tmpl, err := template.New(string(name)).Parse(string(content))
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %s: %w", name, err)
		}

		r.templates[name] = tmpl
	}

	return r, nil
}

// Render renders the specified template with the given data
func (r *Renderer) Render(templateName StateTemplate, data *TemplateData) (string, error) {
	tmpl, exists := r.templates[templateName]
	if !exists {
		return "", fmt.Errorf("template %s not found", templateName)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render template %s: %w", templateName, err)
	}

	return buf.String(), nil
}

// GetAvailableTemplates returns a list of all available templates
func (r *Renderer) GetAvailableTemplates() []StateTemplate {
	templates := make([]StateTemplate, 0, len(r.templates))
	for name := range r.templates {
		templates = append(templates, name)
	}
	return templates
}
