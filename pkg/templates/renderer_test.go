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

	// Check that all expected templates are loaded
	expectedTemplates := []StateTemplate{
		// Coding agent templates
		PlanningTemplate,
		CodingTemplate,
		TestingTemplate,
		ApprovalTemplate,
		// Architect agent templates
		SpecAnalysisTemplate,
		StoryGenerationTemplate,
		TechnicalQATemplate,
		CodeReviewTemplate,
	}

	for _, templateName := range expectedTemplates {
		data := &TemplateData{
			TaskContent: "Test task",
			Context:     "Test context",
		}
		_, err := renderer.Render(templateName, data)
		if err != nil {
			t.Errorf("Failed to render template %s: %v", templateName, err)
		}
	}
}

func TestRenderPlanningTemplate(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := &TemplateData{
		TaskContent: "Create a health endpoint",
		Context:     "Go web service",
	}

	result, err := renderer.Render(PlanningTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render planning template: %v", err)
	}

	// Verify all placeholders were replaced
	if strings.Contains(result, "{{.TaskContent}}") {
		t.Error("Template placeholder {{.TaskContent}} was not replaced")
	}
	if strings.Contains(result, "{{.Context}}") {
		t.Error("Template placeholder {{.Context}} was not replaced")
	}

	// Verify content insertion
	if !strings.Contains(result, data.TaskContent) {
		t.Error("Template should contain task content")
	}
	if !strings.Contains(result, data.Context) {
		t.Error("Template should contain context")
	}

	// Verify template contains MCP tools guidance
	if !strings.Contains(result, "MCP tools") {
		t.Error("Template should mention MCP tools")
	}
	if !strings.Contains(result, `<tool name="shell">`) {
		t.Error("Template should contain shell tool example")
	}
}

func TestRenderCodingTemplate(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := &TemplateData{
		TaskContent: "Create a health endpoint",
		Context:     "Go web service",
		Plan:        "1. Create handler function 2. Add route 3. Test endpoint",
	}

	result, err := renderer.Render(CodingTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render coding template: %v", err)
	}

	// Verify all placeholders were replaced
	if strings.Contains(result, "{{.Plan}}") {
		t.Error("Template placeholder {{.Plan}} was not replaced")
	}
	if strings.Contains(result, "{{.TaskContent}}") {
		t.Error("Template placeholder {{.TaskContent}} was not replaced")
	}

	// Verify content insertion
	if !strings.Contains(result, data.Plan) {
		t.Error("Template should contain plan content")
	}
	if !strings.Contains(result, data.TaskContent) {
		t.Error("Template should contain task content")
	}
}

func TestRenderApprovalTemplate(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := &TemplateData{
		TaskContent:    "Create a health endpoint",
		Context:        "Go web service",
		Implementation: "func healthHandler(w http.ResponseWriter, r *http.Request) { ... }",
	}

	result, err := renderer.Render(ApprovalTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render approval template: %v", err)
	}

	// Verify content insertion
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
		Context:     "Go microservice architecture",
	}

	for _, templateName := range architectTemplates {
		result, err := renderer.Render(templateName, data)
		if err != nil {
			t.Errorf("Failed to render architect template %s: %v", templateName, err)
		}
		
		// Basic verification that template was processed
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

	// Test with comprehensive data
	data := &TemplateData{
		TaskContent:    "Create a comprehensive REST API",
		Context:        "Go microservice with PostgreSQL",
		Plan:           "1. Set up database models 2. Create handlers 3. Add middleware 4. Write tests",
		ToolResults:    "Database connected, tables created successfully",
		Implementation: "Complete REST API with CRUD operations",
		TestResults:    "All tests passed: 15/15",
		WorkDir:        "/workspace/api-service",
		Extra: map[string]interface{}{
			"custom_field": "custom_value",
		},
	}

	// Test each template can handle complete data
	templates := []StateTemplate{
		PlanningTemplate,
		CodingTemplate,
		TestingTemplate,
		ApprovalTemplate,
	}

	for _, templateName := range templates {
		result, err := renderer.Render(templateName, data)
		if err != nil {
			t.Errorf("Template %s failed with complete data: %v", templateName, err)
		}
		
		// Verify no unprocessed placeholders remain
		if strings.Contains(result, "{{.") {
			t.Errorf("Template %s contains unprocessed placeholders", templateName)
		}
	}
}