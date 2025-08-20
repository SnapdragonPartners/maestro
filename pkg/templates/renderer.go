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
	Extra             map[string]any `json:"extra,omitempty"`
	TaskContent       string         `json:"task_content"`
	Plan              string         `json:"plan,omitempty"`
	ToolResults       string         `json:"tool_results,omitempty"`
	Implementation    string         `json:"implementation,omitempty"`
	TestResults       string         `json:"test_results,omitempty"`
	WorkDir           string         `json:"work_dir,omitempty"`
	TreeOutput        string         `json:"tree_output,omitempty"`
	ToolDocumentation string         `json:"tool_documentation,omitempty"`
	// Build commands from project configuration
	BuildCommand string `json:"build_command,omitempty"`
	TestCommand  string `json:"test_command,omitempty"`
	LintCommand  string `json:"lint_command,omitempty"`
	RunCommand   string `json:"run_command,omitempty"`
}

// StateTemplate represents a workflow state template.
type StateTemplate string

const (
	// DevOpsPlanningTemplate is the template for DevOps planning state.
	DevOpsPlanningTemplate StateTemplate = "devops_planning.tpl.md"
	// AppPlanningTemplate is the template for App planning state.
	AppPlanningTemplate StateTemplate = "app_planning.tpl.md"
	// DevOpsCodingTemplate is the template for DevOps coding tasks.
	DevOpsCodingTemplate StateTemplate = "devops_coding.tpl.md"
	// AppCodingTemplate is the template for App coding tasks.
	AppCodingTemplate StateTemplate = "app_coding.tpl.md"
	// TestingTemplate is the template for coder testing state.
	TestingTemplate StateTemplate = "testing.tpl.md"
	// ApprovalTemplate is the template for code approval requests.
	ApprovalTemplate StateTemplate = "approval.tpl.md"
	// AppCompletionApprovalTemplate is the template for app story completion approval.
	AppCompletionApprovalTemplate StateTemplate = "app_completion_approval.tpl.md"
	// DevOpsCompletionApprovalTemplate is the template for devops story completion approval.
	DevOpsCompletionApprovalTemplate StateTemplate = "devops_completion_approval.tpl.md"
	// TestFailureInstructionsTemplate is the mini-template for app test failure instructions.
	TestFailureInstructionsTemplate StateTemplate = "test_failure_instructions.tpl.md"
	// DevOpsTestFailureInstructionsTemplate is the mini-template for devops test failure instructions.
	DevOpsTestFailureInstructionsTemplate StateTemplate = "devops_test_failure_instructions.tpl.md"
	// BudgetReviewFeedbackTemplate is the mini-template for budget review feedback.
	BudgetReviewFeedbackTemplate StateTemplate = "budget_review_feedback.tpl.md"
	// MergeFailureFeedbackTemplate is the mini-template for merge failure feedback.
	MergeFailureFeedbackTemplate StateTemplate = "merge_failure_feedback.tpl.md"
	// GitCommitFailureTemplate is the mini-template for git commit failures.
	GitCommitFailureTemplate StateTemplate = "git_commit_failure.tpl.md"
	// GitPushFailureTemplate is the mini-template for git push failures.
	GitPushFailureTemplate StateTemplate = "git_push_failure.tpl.md"
	// PRCreationFailureTemplate is the mini-template for pull request creation failures.
	PRCreationFailureTemplate StateTemplate = "pr_creation_failure.tpl.md"
	// GitConfigFailureTemplate is the mini-template for git configuration failures.
	GitConfigFailureTemplate StateTemplate = "git_config_failure.tpl.md"
	// GitHubAuthFailureTemplate is the mini-template for GitHub authentication failures.
	GitHubAuthFailureTemplate StateTemplate = "github_auth_failure.tpl.md"
	// AppCodeReviewTemplate is the template for app story code review approval.
	AppCodeReviewTemplate StateTemplate = "app_code_review.tpl.md"
	// DevOpsCodeReviewTemplate is the template for devops story code review approval.
	DevOpsCodeReviewTemplate StateTemplate = "devops_code_review.tpl.md"

	// BudgetReviewPlanningTemplate is the template for architect budget review in planning state.
	BudgetReviewPlanningTemplate StateTemplate = "budget_review_planning.tpl.md"
	// BudgetReviewCodingTemplate is the template for architect budget review in coding state.
	BudgetReviewCodingTemplate StateTemplate = "budget_review_coding.tpl.md"

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
		DevOpsPlanningTemplate,
		AppPlanningTemplate,
		DevOpsCodingTemplate,
		AppCodingTemplate,
		TestingTemplate,
		ApprovalTemplate,
		TestFailureInstructionsTemplate,
		DevOpsTestFailureInstructionsTemplate,
		BudgetReviewFeedbackTemplate,
		MergeFailureFeedbackTemplate,
		GitCommitFailureTemplate,
		GitPushFailureTemplate,
		PRCreationFailureTemplate,
		GitConfigFailureTemplate,
		GitHubAuthFailureTemplate,
		// Architect agent templates.
		BudgetReviewPlanningTemplate,
		BudgetReviewCodingTemplate,
		SpecAnalysisTemplate,
		StoryGenerationTemplate,
		TechnicalQATemplate,
		CodeReviewTemplate,
		AppCodeReviewTemplate,
		DevOpsCodeReviewTemplate,
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

// RenderSimple renders a template with simple data - helper for mini-templates.
// The data will be available as .Data in the template.
func (r *Renderer) RenderSimple(templateName StateTemplate, data any) (string, error) {
	templateData := &TemplateData{
		Extra: map[string]any{
			"Data": data,
		},
	}
	return r.Render(templateName, templateData)
}
