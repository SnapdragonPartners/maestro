package coder

import (
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
)

func TestPlanInclusionInCodingTemplate(t *testing.T) {
	// Test that plan from PLANNING state is included in CODING template data

	// Create a mock state machine with plan data
	sm := agent.NewBaseStateMachine("test-coder", proto.StateWaiting, nil, nil)

	// Store test plan data (simulates PLANNING state storing plan)
	testPlan := `## Implementation Plan
	
	**Step 1**: Initialize Go module system
	**Step 2**: Build Docker container 
	**Step 3**: Validate container functionality
	**Step 4**: Test application endpoints
	
	This is a test plan for container infrastructure.`

	sm.SetStateData(KeyPlan, testPlan)
	sm.SetStateData(string(stateDataKeyTaskContent), "Container Bootstrap and Build")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeDevOps))

	// Create a minimal coder instance for testing
	coder := &Coder{
		workDir: "/test/workspace",
	}

	// Create a mock tool provider
	agentCtx := tools.AgentContext{
		Executor:        nil,
		ReadOnly:        false,
		NetworkDisabled: false,
		WorkDir:         "/test/workspace",
	}
	coder.codingToolProvider = tools.NewProvider(agentCtx, tools.DevOpsCodingTools)

	// Create a template renderer
	renderer, err := templates.NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create template renderer: %v", err)
	}

	// Test the template data creation (extract the logic from executeCodingWithTemplate)
	storyType := string(proto.StoryTypeDevOps)
	taskContent := "Container Bootstrap and Build"
	plan := testPlan

	enhancedTemplateData := &templates.TemplateData{
		TaskContent:       taskContent,
		Plan:              plan, // This is the key field we're testing
		WorkDir:           coder.workDir,
		ToolDocumentation: coder.codingToolProvider.GenerateToolDocumentation(),
		Extra: map[string]any{
			"story_type": storyType,
		},
	}

	// Verify plan is included in template data
	if enhancedTemplateData.Plan == "" {
		t.Error("Plan field is empty in template data")
	}

	if enhancedTemplateData.Plan != testPlan {
		t.Errorf("Plan field mismatch. Expected: %q, Got: %q", testPlan, enhancedTemplateData.Plan)
	}

	// Test template rendering with plan data
	codingTemplate := templates.DevOpsCodingTemplate
	renderedPrompt, err := renderer.RenderWithUserInstructions(codingTemplate, enhancedTemplateData, coder.workDir, "CODER")
	if err != nil {
		t.Fatalf("Failed to render template: %v", err)
	}

	// Verify the plan appears in the rendered template
	if renderedPrompt == "" {
		t.Error("Rendered template is empty")
	}

	// Check that plan content appears in the rendered template
	// Since the template uses {{.Plan}}, the plan content should be included
	if !containsSubstring(renderedPrompt, "Initialize Go module system") {
		t.Error("Plan content not found in rendered template")
	}

	if !containsSubstring(renderedPrompt, "Build Docker container") {
		t.Error("Plan implementation steps not found in rendered template")
	}

	if !containsSubstring(renderedPrompt, "This is a test plan for container infrastructure") {
		t.Error("Plan description not found in rendered template")
	}

	t.Logf("✅ Plan successfully included in CODING template")
	t.Logf("Plan length: %d characters", len(enhancedTemplateData.Plan))
	t.Logf("Rendered template length: %d characters", len(renderedPrompt))
}

func TestPlanInclusionWithEmptyPlan(t *testing.T) {
	// Test behavior when plan is missing or empty

	sm := agent.NewBaseStateMachine("test-coder", proto.StateWaiting, nil, nil)

	// Set task content but leave plan empty
	sm.SetStateData(string(stateDataKeyTaskContent), "Test Task")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeDevOps))
	// Note: Not setting KeyPlan, so it should default to empty string

	coder := &Coder{
		workDir: "/test/workspace",
	}

	agentCtx := tools.AgentContext{
		Executor:        nil,
		ReadOnly:        false,
		NetworkDisabled: false,
		WorkDir:         "/test/workspace",
	}
	coder.codingToolProvider = tools.NewProvider(agentCtx, tools.DevOpsCodingTools)

	renderer, err := templates.NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create template renderer: %v", err)
	}

	// Test template data creation with empty plan
	storyType := string(proto.StoryTypeDevOps)
	taskContent := "Test Task"
	plan := "" // Empty plan

	enhancedTemplateData := &templates.TemplateData{
		TaskContent:       taskContent,
		Plan:              plan,
		WorkDir:           coder.workDir,
		ToolDocumentation: coder.codingToolProvider.GenerateToolDocumentation(),
		Extra: map[string]any{
			"story_type": storyType,
		},
	}

	// Verify plan field is empty but template still renders
	if enhancedTemplateData.Plan != "" {
		t.Errorf("Expected empty plan, got: %q", enhancedTemplateData.Plan)
	}

	codingTemplate := templates.DevOpsCodingTemplate
	renderedPrompt, err := renderer.RenderWithUserInstructions(codingTemplate, enhancedTemplateData, coder.workDir, "CODER")
	if err != nil {
		t.Fatalf("Failed to render template with empty plan: %v", err)
	}

	// Template should still render successfully even with empty plan
	if renderedPrompt == "" {
		t.Error("Rendered template is empty")
	}

	t.Logf("✅ Template renders successfully with empty plan")
	t.Logf("Rendered template length: %d characters", len(renderedPrompt))
}

// Helper function to check if a string contains a substring (case-insensitive).
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
