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
		// Architect agent templates.
		SpecAnalysisTemplate,
		StoryGenerationTemplate,
		TechnicalQATemplate,
		CodeReviewTemplate,
	}

	for _, templateName := range expectedTemplates {
		data := &TemplateData{
			TaskContent: "Test task",
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
