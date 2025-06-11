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
		PlanningTemplate,
		ToolInvocationTemplate,
		CodingTemplate,
		TestingTemplate,
		ApprovalTemplate,
	}

	availableTemplates := renderer.GetAvailableTemplates()
	if len(availableTemplates) != len(expectedTemplates) {
		t.Errorf("Expected %d templates, got %d", len(expectedTemplates), len(availableTemplates))
	}

	for _, expected := range expectedTemplates {
		found := false
		for _, available := range availableTemplates {
			if available == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected template %s not found", expected)
		}
	}
}

func TestRenderPlanningTemplate(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := &TemplateData{
		TaskContent: "Create a health endpoint that returns JSON with status and timestamp",
		Context:     "Working on a Go web service project",
	}

	result, err := renderer.Render(PlanningTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render planning template: %v", err)
	}

	// Verify template placeholders were replaced
	if strings.Contains(result, "{{.TaskContent}}") {
		t.Error("Template placeholder {{.TaskContent}} was not replaced")
	}
	if strings.Contains(result, "{{.Context}}") {
		t.Error("Template placeholder {{.Context}} was not replaced")
	}

	// Verify content was inserted
	if !strings.Contains(result, data.TaskContent) {
		t.Error("Task content was not inserted into template")
	}
	if !strings.Contains(result, data.Context) {
		t.Error("Context was not inserted into template")
	}

	// Verify template structure
	if !strings.Contains(result, "# Planning Phase") {
		t.Error("Template should contain planning phase header")
	}
	if !strings.Contains(result, "MCP tools") {
		t.Error("Template should mention MCP tools")
	}
	if !strings.Contains(result, `<tool name="shell">`) {
		t.Error("Template should contain shell tool example")
	}
}

func TestRenderToolInvocationTemplate(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := &TemplateData{
		TaskContent: "Create a health endpoint",
		Context:     "Go web service",
		Plan:        "1. Create handler function 2. Add route 3. Test endpoint",
	}

	result, err := renderer.Render(ToolInvocationTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render tool invocation template: %v", err)
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
		t.Error("Plan was not inserted into template")
	}

	// Verify template structure
	if !strings.Contains(result, "# Tool Invocation Phase") {
		t.Error("Template should contain tool invocation phase header")
	}
	if !strings.Contains(result, `<tool name="shell">`) {
		t.Error("Template should contain shell tool usage")
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
		Plan:        "Implementation plan here",
		ToolResults: "Environment is ready, found existing patterns",
	}

	result, err := renderer.Render(CodingTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render coding template: %v", err)
	}

	// Verify template structure
	if !strings.Contains(result, "# Coding Phase") {
		t.Error("Template should contain coding phase header")
	}
	if !strings.Contains(result, "Go best practices") {
		t.Error("Template should mention Go best practices")
	}
	if !strings.Contains(result, `"implementation"`) {
		t.Error("Template should specify implementation response format")
	}

	// Verify content insertion
	if !strings.Contains(result, data.ToolResults) {
		t.Error("Tool results were not inserted into template")
	}
}

func TestRenderTestingTemplate(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := &TemplateData{
		TaskContent:    "Create a health endpoint",
		Context:        "Go web service",
		Implementation: "Created health.go with handler function",
	}

	result, err := renderer.Render(TestingTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render testing template: %v", err)
	}

	// Verify template structure
	if !strings.Contains(result, "# Testing Phase") {
		t.Error("Template should contain testing phase header")
	}
	if !strings.Contains(result, "go test") {
		t.Error("Template should mention go test command")
	}
	if !strings.Contains(result, `"test_results"`) {
		t.Error("Template should specify test results format")
	}

	// Verify content insertion
	if !strings.Contains(result, data.Implementation) {
		t.Error("Implementation details were not inserted into template")
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
		Implementation: "Created health.go with handler",
		TestResults:    "All tests passed",
	}

	result, err := renderer.Render(ApprovalTemplate, data)
	if err != nil {
		t.Fatalf("Failed to render approval template: %v", err)
	}

	// Verify template structure
	if !strings.Contains(result, "# Approval Phase") {
		t.Error("Template should contain approval phase header")
	}
	if !strings.Contains(result, "AWAIT_APPROVAL") {
		t.Error("Template should mention AWAIT_APPROVAL state")
	}
	if !strings.Contains(result, `"completion_summary"`) {
		t.Error("Template should specify completion summary format")
	}

	// Verify content insertion
	if !strings.Contains(result, data.TestResults) {
		t.Error("Test results were not inserted into template")
	}
}

func TestRenderInvalidTemplate(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := &TemplateData{
		TaskContent: "Test task",
		Context:     "Test context",
	}

	_, err = renderer.Render("nonexistent.tpl.md", data)
	if err == nil {
		t.Error("Expected error when rendering non-existent template")
	}
}

func TestTemplateDataComplete(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	// Test with all fields populated
	data := &TemplateData{
		TaskContent:    "Create a health endpoint",
		Context:        "Go web service project",
		Plan:           "Step 1: Create handler, Step 2: Add route",
		ToolResults:    "Found existing server setup",
		Implementation: "Created health.go with proper handler",
		TestResults:    "All tests passing, coverage 95%",
		Extra: map[string]interface{}{
			"custom_field": "custom_value",
		},
	}

	// Test each template can handle complete data
	templates := []StateTemplate{
		PlanningTemplate,
		ToolInvocationTemplate,
		CodingTemplate,
		TestingTemplate,
		ApprovalTemplate,
	}

	for _, templateName := range templates {
		result, err := renderer.Render(templateName, data)
		if err != nil {
			t.Errorf("Failed to render template %s with complete data: %v", templateName, err)
		}
		if len(result) == 0 {
			t.Errorf("Template %s rendered empty result", templateName)
		}
	}
}

func TestTemplateValidPromptStructure(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	data := &TemplateData{
		TaskContent: "Create a health endpoint that returns JSON with status and timestamp",
		Context:     "Working on a Go web service project with existing HTTP server setup",
	}

	// Test each template produces a valid prompt structure
	testCases := []struct {
		template     StateTemplate
		shouldContain []string
	}{
		{
			template: PlanningTemplate,
			shouldContain: []string{
				"PLANNING state",
				"Analyze the task requirements",
				"<tool name=",
				"JSON object",
				"next_action",
			},
		},
		{
			template: ToolInvocationTemplate,
			shouldContain: []string{
				"TOOL_INVOCATION state",
				"Use MCP tools",
				"<tool name=\"shell\">",
				"tools_executed",
				"environment_ready",
			},
		},
		{
			template: CodingTemplate,
			shouldContain: []string{
				"CODING state",
				"Implement the solution",
				"Go best practices",
				"\"implementation\"",
				"\"files\"",
			},
		},
		{
			template: TestingTemplate,
			shouldContain: []string{
				"TESTING state",
				"test the implemented solution",
				"go test",
				"test_results",
				"compilation",
			},
		},
		{
			template: ApprovalTemplate,
			shouldContain: []string{
				"AWAIT_APPROVAL state",
				"present your completed implementation",
				"completion_summary",
				"ready_for_review",
			},
		},
	}

	for _, tc := range testCases {
		result, err := renderer.Render(tc.template, data)
		if err != nil {
			t.Errorf("Failed to render template %s: %v", tc.template, err)
			continue
		}

		for _, expected := range tc.shouldContain {
			if !strings.Contains(result, expected) {
				t.Errorf("Template %s should contain '%s' but doesn't", tc.template, expected)
			}
		}
	}
}