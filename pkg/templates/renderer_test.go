package templates

import (
	"strings"
	"testing"
)

func TestNewRenderer(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	if renderer == nil {
		t.Fatal("Expected non-nil renderer")
	}

	// Check that all expected templates are loaded.
	expectedTemplates := []StateTemplate{
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

	for _, templateName := range expectedTemplates {
		data := &TemplateData{
			TaskContent: "Test task",
			Extra: map[string]any{
				"Data":    "Test data",
				"Content": "Test content",
			},
		}

		// Special handling for git config template which needs structured data
		if templateName == GitConfigFailureTemplate {
			data.Extra["Data"] = map[string]string{
				"Error":        "Test error",
				"GitUserName":  "Test User",
				"GitUserEmail": "test@example.com",
			}
		}

		_, err := renderer.Render(templateName, data)
		if err != nil {
			t.Errorf("Failed to render template %s: %v", templateName, err)
		}
	}
}

func TestRenderAppPlanningTemplate(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := &TemplateData{
		TaskContent:       "Create a health endpoint",
		ToolDocumentation: "## Available Tools\n\n### shell\nExecute shell commands in the workspace.",
	}

	result, err := renderer.Render(AppPlanningTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render app planning template: %v", err)
	}

	// Verify all placeholders were replaced.
	if strings.Contains(result, "{{.TaskContent}}") {
		t.Error("Template placeholder {{.TaskContent}} was not replaced")
	}

	// Verify content insertion.
	if !strings.Contains(result, data.TaskContent) {
		t.Error("Template should contain task content")
	}

	// Verify template contains app-specific guidance.
	if !strings.Contains(result, "Application Development Planning") {
		t.Error("Template should contain app planning title")
	}
	if !strings.Contains(result, "Available Tools") {
		t.Error("Template should mention available tools")
	}
	if !strings.Contains(result, "shell") {
		t.Error("Template should contain shell tool")
	}
}

func TestRenderDevOpsPlanningTemplate(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := &TemplateData{
		TaskContent:       "Build and validate Docker container",
		ToolDocumentation: "## Available Tools\n\n### container_build\nBuild Docker containers.",
	}

	result, err := renderer.Render(DevOpsPlanningTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render DevOps planning template: %v", err)
	}

	// Verify DevOps-specific content
	if !strings.Contains(result, data.TaskContent) {
		t.Error("Template should contain task content")
	}

	if !strings.Contains(result, "DevOps Infrastructure Planning") {
		t.Error("Template should contain DevOps planning title")
	}

	if !strings.Contains(result, "container_build") {
		t.Error("Template should mention container_build tool")
	}

	if !strings.Contains(result, "Infrastructure Exploration") {
		t.Error("Template should contain infrastructure exploration section")
	}
}

func TestRenderAppCodingTemplate(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := &TemplateData{
		TaskContent: "Create a health endpoint",
		Plan:        "1. Create handler function 2. Add route 3. Test endpoint",
	}

	result, err := renderer.Render(AppCodingTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render coding template: %v", err)
	}

	// Verify all placeholders were replaced.
	if strings.Contains(result, "{{.Plan}}") {
		t.Error("Template placeholder {{.Plan}} was not replaced")
	}
	if strings.Contains(result, "{{.TaskContent}}") {
		t.Error("Template placeholder {{.TaskContent}} was not replaced")
	}

	// Verify content insertion.
	if !strings.Contains(result, data.Plan) {
		t.Error("Template should contain plan content")
	}
	if !strings.Contains(result, data.TaskContent) {
		t.Error("Template should contain task content")
	}
}

func TestRenderDevOpsCodingTemplate(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := &TemplateData{
		TaskContent: "Build and validate Docker container",
		Plan:        "1. Build container 2. Test container 3. Validate health check",
	}

	result, err := renderer.Render(DevOpsCodingTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render DevOps coding template: %v", err)
	}

	// Verify DevOps-specific content
	if !strings.Contains(result, data.TaskContent) {
		t.Error("Template should contain task content")
	}

	if !strings.Contains(result, data.Plan) {
		t.Error("Template should contain plan")
	}

	if !strings.Contains(result, "DevOps Implementation Guidelines") {
		t.Error("Template should contain DevOps guidelines")
	}

	if !strings.Contains(result, "container_build") {
		t.Error("Template should mention container_build tool")
	}
}

func TestRenderApprovalTemplate(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := &TemplateData{
		TaskContent:    "Create a health endpoint",
		Implementation: "func healthHandler(w http.ResponseWriter, r *http.Request) { ... }",
	}

	result, err := renderer.Render(ApprovalTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render approval template: %v", err)
	}

	// Verify content insertion.
	if !strings.Contains(result, data.Implementation) {
		t.Error("Template should contain implementation content")
	}
}

func TestRenderArchitectTemplates(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	architectTemplates := []StateTemplate{
		SpecAnalysisTemplate,
		StoryGenerationTemplate,
		TechnicalQATemplate,
		CodeReviewTemplate,
	}

	data := &TemplateData{
		TaskContent: "Analyze requirements for health endpoint",
	}

	for _, templateName := range architectTemplates {
		result, err := renderer.Render(templateName, data)
		if err != nil {
			t.Errorf("Failed to render architect template %s: %v", templateName, err)
		}

		// Basic verification that template was processed.
		if strings.Contains(result, "{{.TaskContent}}") {
			t.Errorf("Template %s still contains unprocessed placeholder", templateName)
		}
	}
}

func TestRenderWithCompleteData(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	// Test with comprehensive data.
	data := &TemplateData{
		TaskContent:    "Create a comprehensive REST API",
		Plan:           "1. Set up database models 2. Create handlers 3. Add middleware 4. Write tests",
		ToolResults:    "Database connected, tables created successfully",
		Implementation: "Complete REST API with CRUD operations",
		TestResults:    "All tests passed: 15/15",
		WorkDir:        "/workspace/api-service",
		Extra: map[string]any{
			"custom_field": "custom_value",
		},
	}

	// Test each template can handle complete data.
	templates := []StateTemplate{
		DevOpsPlanningTemplate,
		AppPlanningTemplate,
		DevOpsCodingTemplate,
		AppCodingTemplate,
		TestingTemplate,
		ApprovalTemplate,
	}

	for _, templateName := range templates {
		result, err := renderer.Render(templateName, data)
		if err != nil {
			t.Errorf("Template %s failed with complete data: %v", templateName, err)
		}

		// Verify no unprocessed placeholders remain.
		if strings.Contains(result, "{{.") {
			t.Errorf("Template %s contains unprocessed placeholders", templateName)
		}
	}
}

// TestRenderSimpleMiniTemplates tests all mini-templates that use RenderSimple.
func TestRenderSimpleMiniTemplates(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	tests := []struct {
		name         string
		template     StateTemplate
		data         any
		expectedText string
	}{
		{
			name:         "Budget Review Feedback",
			template:     BudgetReviewFeedbackTemplate,
			data:         "Please reduce complexity and focus on core requirements",
			expectedText: "ARCHITECT GUIDANCE",
		},
		{
			name:         "Git Push Failure",
			template:     GitPushFailureTemplate,
			data:         "fatal: could not read from remote repository",
			expectedText: "Git Push Failed",
		},
		{
			name:         "Git Commit Failure",
			template:     GitCommitFailureTemplate,
			data:         "error: nothing to commit",
			expectedText: "Git Commit Failed",
		},
		{
			name:         "GitHub Auth Failure",
			template:     GitHubAuthFailureTemplate,
			data:         "authentication failed",
			expectedText: "GitHub Authentication Setup Failed",
		},
		{
			name:         "Merge Failure Feedback",
			template:     MergeFailureFeedbackTemplate,
			data:         "merge conflict in main.go",
			expectedText: "MERGE FAILED - Changes Required",
		},
		{
			name:         "PR Creation Failure",
			template:     PRCreationFailureTemplate,
			data:         "failed to create pull request",
			expectedText: "Pull Request Creation Failed",
		},
		{
			name:         "Test Failure Instructions",
			template:     TestFailureInstructionsTemplate,
			data:         "TestHealthEndpoint failed",
			expectedText: "Tests are failing and must pass",
		},
		{
			name:         "DevOps Test Failure Instructions",
			template:     DevOpsTestFailureInstructionsTemplate,
			data:         "Container build failed",
			expectedText: "Infrastructure tests are failing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := renderer.RenderSimple(tt.template, tt.data)
			if err != nil {
				t.Errorf("Failed to render template %s: %v", tt.template, err)
				return
			}

			// Verify template contains expected text
			if !strings.Contains(result, tt.expectedText) {
				t.Errorf("Template %s should contain '%s', got: %s", tt.template, tt.expectedText, result)
			}

			// Verify the data was inserted correctly
			if dataStr, ok := tt.data.(string); ok {
				if !strings.Contains(result, dataStr) {
					t.Errorf("Template %s should contain data '%s', got: %s", tt.template, tt.data, result)
				}
			}

			// Verify no unprocessed placeholders remain
			if strings.Contains(result, "{{.Data}}") {
				t.Errorf("Template %s still contains unprocessed {{.Data}} placeholder", tt.template)
			}

			if dataStr, ok := tt.data.(string); ok && strings.Contains(result, "{{.Extra.Data}}") && !strings.Contains(result, dataStr) {
				t.Errorf("Template %s contains unprocessed {{.Extra.Data}} placeholder", tt.template)
			}
		})
	}
}

// TestGitConfigFailureTemplate tests the special git config template with structured data.
func TestGitConfigFailureTemplate(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	templateData := map[string]string{
		"Error":        "user.name not configured",
		"GitUserName":  "Test User",
		"GitUserEmail": "test@example.com",
	}

	result, err := renderer.RenderSimple(GitConfigFailureTemplate, templateData)
	if err != nil {
		t.Fatalf("Failed to render git config failure template: %v", err)
	}

	// Verify template contains expected text
	if !strings.Contains(result, "Git Configuration Failed") {
		t.Error("Template should contain 'Git Configuration Failed'")
	}

	// Verify the structured data was inserted correctly
	if !strings.Contains(result, templateData["Error"]) {
		t.Error("Template should contain error message")
	}
	if !strings.Contains(result, templateData["GitUserName"]) {
		t.Error("Template should contain git user name")
	}
	if !strings.Contains(result, templateData["GitUserEmail"]) {
		t.Error("Template should contain git user email")
	}

	// Verify git commands are properly formatted
	if !strings.Contains(result, `git config --global user.name "Test User"`) {
		t.Error("Template should contain properly formatted git config name command")
	}
	if !strings.Contains(result, `git config --global user.email "test@example.com"`) {
		t.Error("Template should contain properly formatted git config email command")
	}

	// Verify no unprocessed placeholders remain
	if strings.Contains(result, "{{.") {
		t.Errorf("Template contains unprocessed placeholders: %s", result)
	}
}

// TestCodeReviewTemplates tests the code review templates that use Extra.Content.
func TestCodeReviewTemplates(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	testContent := `
package main

import "net/http"

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
`

	tests := []struct {
		name         string
		template     StateTemplate
		expectedText string
	}{
		{
			name:         "App Code Review",
			template:     AppCodeReviewTemplate,
			expectedText: "Application Code Review",
		},
		{
			name:         "DevOps Code Review",
			template:     DevOpsCodeReviewTemplate,
			expectedText: "DevOps Code Review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create template data with content in Extra map
			templateData := &TemplateData{
				Extra: map[string]any{
					"Content": testContent,
				},
			}

			result, err := renderer.Render(tt.template, templateData)
			if err != nil {
				t.Errorf("Failed to render template %s: %v", tt.template, err)
				return
			}

			// Verify template contains expected text
			if !strings.Contains(result, tt.expectedText) {
				t.Errorf("Template %s should contain '%s'", tt.template, tt.expectedText)
			}

			// Verify the code content was inserted correctly
			if !strings.Contains(result, "func healthHandler") {
				t.Errorf("Template %s should contain the code content", tt.template)
			}

			// Verify no unprocessed placeholders remain
			if strings.Contains(result, "{{.Content}}") {
				t.Errorf("Template %s still contains unprocessed {{.Content}} placeholder", tt.template)
			}

			if strings.Contains(result, "{{.Extra.Content}}") && !strings.Contains(result, "func healthHandler") {
				t.Errorf("Template %s contains unprocessed {{.Extra.Content}} placeholder", tt.template)
			}
		})
	}
}
