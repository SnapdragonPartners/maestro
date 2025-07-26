// Package templates provides template rendering functionality for agent prompts and workflows.
package templates

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"

	"orchestrator/pkg/utils"
)

//go:embed *.tpl.md
var templateFS embed.FS

// TemplateData holds the data for template rendering.
type TemplateData struct {
	Extra          map[string]any `json:"extra,omitempty"`
	TaskContent    string         `json:"task_content"`
	Plan           string         `json:"plan,omitempty"`
	ToolResults    string         `json:"tool_results,omitempty"`
	Implementation string         `json:"implementation,omitempty"`
	TestResults    string         `json:"test_results,omitempty"`
	WorkDir        string         `json:"work_dir,omitempty"`
	TreeOutput     string         `json:"tree_output,omitempty"`
}

// StateTemplate represents a workflow state template.
type StateTemplate string

const (
	// PlanningTemplate is the template for coder planning state.
	PlanningTemplate StateTemplate = "planning.tpl.md"
	// CodingTemplate is the template for coder coding state.
	CodingTemplate StateTemplate = "coding.tpl.md"
	// TestingTemplate is the template for coder testing state.
	TestingTemplate StateTemplate = "testing.tpl.md"
	// ApprovalTemplate is the template for code approval requests.
	ApprovalTemplate StateTemplate = "approval.tpl.md"

	// SpecAnalysisTemplate is the template for architect spec analysis state.
	SpecAnalysisTemplate StateTemplate = "spec_analysis.tpl.md"
	// StoryGenerationTemplate is the template for architect story generation state.
	StoryGenerationTemplate StateTemplate = "story_generation.tpl.md"
	// TechnicalQATemplate is the template for architect technical Q&A state.
	TechnicalQATemplate StateTemplate = "technical_qa.tpl.md"
	// CodeReviewTemplate is the template for architect code review state.
	CodeReviewTemplate StateTemplate = "code_review.tpl.md"
)

// Renderer handles template rendering for workflow states.
type Renderer struct {
	templates map[StateTemplate]*template.Template
}

// NewRenderer creates a new template renderer.
func NewRenderer() (*Renderer, error) {
	r := &Renderer{
		templates: make(map[StateTemplate]*template.Template),
	}

	// Load all templates.
	templateNames := []StateTemplate{
		// Coding agent templates.
		PlanningTemplate,
		CodingTemplate,
		TestingTemplate,
		ApprovalTemplate,
		// Architect agent templates.
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

		tmpl, err := template.New(string(name)).Funcs(template.FuncMap{
			"contains": strings.Contains,
		}).Parse(string(content))
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %s: %w", name, err)
		}

		r.templates[name] = tmpl
	}

	return r, nil
}

// Render renders the specified template with the given data.
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

// RenderWithUserInstructions renders the specified template with user instruction files appended.
// workDir is the working directory containing the .maestro directory.
// agentType should be "CODER" or "ARCHITECT".
func (r *Renderer) RenderWithUserInstructions(templateName StateTemplate, data *TemplateData, workDir, agentType string) (string, error) {
	// First render the base template
	basePrompt, err := r.Render(templateName, data)
	if err != nil {
		return "", err
	}

	// Load user instructions
	instructions, err := utils.LoadUserInstructions(workDir)
	if err != nil {
		return "", fmt.Errorf("failed to load user instructions: %w", err)
	}

	// Format user instructions for the agent type
	userInstructionsFormatted := utils.FormatUserInstructions(instructions, agentType)

	// Append user instructions if they exist
	if userInstructionsFormatted != "" {
		return basePrompt + userInstructionsFormatted, nil
	}

	return basePrompt, nil
}

// GetAvailableTemplates returns a list of all available templates.
func (r *Renderer) GetAvailableTemplates() []StateTemplate {
	templates := make([]StateTemplate, 0, len(r.templates))
	for name := range r.templates {
		templates = append(templates, name)
	}
	return templates
}
