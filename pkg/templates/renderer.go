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

//go:embed *.tpl.md pm/*.tpl.md
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
	// Container information
	ContainerName       string `json:"container_name,omitempty"`
	ContainerDockerfile string `json:"container_dockerfile,omitempty"`
	// Dockerfile content for DevOps review templates
	DockerfileContent string `json:"dockerfile_content,omitempty"`
	// PM agent interview data
	Expertise           string              `json:"expertise,omitempty"`            // User expertise level: NON_TECHNICAL, BASIC, EXPERT
	ConversationHistory []map[string]string `json:"conversation_history,omitempty"` // PM conversation messages
	TurnCount           int                 `json:"turn_count,omitempty"`           // Current turn number
	MaxTurns            int                 `json:"max_turns,omitempty"`            // Maximum turns allowed
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
	// TechnicalQATemplate is the template for architect technical Q&A state.
	TechnicalQATemplate StateTemplate = "technical_qa.tpl.md"
	// CodeReviewTemplate is the template for architect code review state.
	CodeReviewTemplate StateTemplate = "code_review.tpl.md"

	// PlanApprovalRequestTemplate is the template for plan approval request content (coder → architect).
	PlanApprovalRequestTemplate StateTemplate = "plan_approval_request.tpl.md"
	// PlanReviewArchitectTemplate is the template for architect's plan review prompt.
	PlanReviewArchitectTemplate StateTemplate = "plan_review_architect.tpl.md"
	// CodeReviewRequestTemplate is the template for code review request content.
	CodeReviewRequestTemplate StateTemplate = "code_review_request.tpl.md"
	// CompletionRequestTemplate is the template for completion request content.
	CompletionRequestTemplate StateTemplate = "completion_request.tpl.md"
	// MergeRequestTemplate is the template for merge request content.
	MergeRequestTemplate StateTemplate = "merge_request.tpl.md"
	// BudgetReviewRequestPlanningTemplate is the template for budget review request content in planning state.
	BudgetReviewRequestPlanningTemplate StateTemplate = "budget_review_request_planning.tpl.md"
	// BudgetReviewRequestCodingTemplate is the template for budget review request content in coding state.
	BudgetReviewRequestCodingTemplate StateTemplate = "budget_review_request_coding.tpl.md"
	// QuestionRequestTemplate is the template for question request content.
	QuestionRequestTemplate StateTemplate = "question_request.tpl.md"

	// PlanApprovalResponseTemplate is the template for plan approval responses (architect → coder).
	PlanApprovalResponseTemplate StateTemplate = "plan_approval_response.tpl.md"
	// CodeReviewResponseTemplate is the template for code review responses.
	CodeReviewResponseTemplate StateTemplate = "code_review_response.tpl.md"
	// CompletionResponseTemplate is the template for completion review responses.
	CompletionResponseTemplate StateTemplate = "completion_response.tpl.md"
	// BudgetReviewResponseTemplate is the template for budget review responses.
	BudgetReviewResponseTemplate StateTemplate = "budget_review_response.tpl.md"

	// PMInterviewStartTemplate is the template for starting PM interviews (deprecated - use PMWorkingTemplate).
	PMInterviewStartTemplate StateTemplate = "pm/interview_start.tpl.md"
	// PMRequirementsGatheringTemplate is the template for ongoing PM requirements gathering (deprecated - use PMWorkingTemplate).
	PMRequirementsGatheringTemplate StateTemplate = "pm/requirements_gathering.tpl.md"
	// PMSpecGenerationTemplate is the template for generating specifications from interviews (deprecated - use PMWorkingTemplate).
	PMSpecGenerationTemplate StateTemplate = "pm/spec_generation.tpl.md"
	// PMWorkingTemplate is the unified template for PM WORKING state (interviewing, drafting, submitting).
	PMWorkingTemplate StateTemplate = "pm/working.tpl.md"
	// PMBootstrapPrerequisitesTemplate is the template for bootstrap prerequisites injected by spec_submit tool.
	PMBootstrapPrerequisitesTemplate StateTemplate = "pm/bootstrap_prerequisites.tpl.md"
	// PMBootstrapGateTemplate is the focused template for bootstrap-only mode (before project is configured).
	PMBootstrapGateTemplate StateTemplate = "pm/bootstrap_gate.tpl.md"

	// ArchitectSystemTemplate is the system prompt for architect agent per-agent contexts.
	ArchitectSystemTemplate StateTemplate = "architect/system_prompt.tmpl"
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
		AppCompletionApprovalTemplate,
		DevOpsCompletionApprovalTemplate,
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
		ArchitectSystemTemplate,
		BudgetReviewPlanningTemplate,
		BudgetReviewCodingTemplate,
		SpecAnalysisTemplate,
		TechnicalQATemplate,
		CodeReviewTemplate,
		AppCodeReviewTemplate,
		DevOpsCodeReviewTemplate,
		// Request content templates (coder → architect).
		PlanApprovalRequestTemplate,
		PlanReviewArchitectTemplate,
		CodeReviewRequestTemplate,
		CompletionRequestTemplate,
		MergeRequestTemplate,
		BudgetReviewRequestPlanningTemplate,
		BudgetReviewRequestCodingTemplate,
		QuestionRequestTemplate,
		// Response content templates (architect → coder).
		PlanApprovalResponseTemplate,
		CodeReviewResponseTemplate,
		CompletionResponseTemplate,
		BudgetReviewResponseTemplate,
		// PM agent templates.
		PMInterviewStartTemplate,
		PMRequirementsGatheringTemplate,
		PMSpecGenerationTemplate,
		PMWorkingTemplate,                // Unified PM template
		PMBootstrapPrerequisitesTemplate, // Bootstrap prerequisites injected by spec_submit
		PMBootstrapGateTemplate,          // Bootstrap-only mode before project configured
	}

	for _, name := range templateNames {
		content, err := templateFS.ReadFile(string(name))
		if err != nil {
			return nil, fmt.Errorf("failed to read template %s: %w", name, err)
		}

		tmpl, err := template.New(string(name)).Funcs(template.FuncMap{
			"contains": strings.Contains,
			"isMap": func(v any) bool {
				if v == nil {
					return false
				}
				switch v.(type) {
				case map[string]any:
					return true
				default:
					return false
				}
			},
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
