package coder

import (
	"strings"
	"testing"

	"orchestrator/pkg/exec"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
)

// TestTemplateToolDocumentationIntegration verifies that templates are correctly
// populated with story-type-specific tool documentation from the ToolProvider system.
func TestTemplateToolDocumentationIntegration(t *testing.T) {
	//nolint:govet // fieldalignment: Test struct alignment is not performance critical
	tests := []struct {
		name          string
		storyType     proto.StoryType
		template      templates.StateTemplate
		expectedTools []string
		state         string
	}{
		{
			name:          "DevOps Planning Template",
			storyType:     proto.StoryTypeDevOps,
			template:      templates.DevOpsPlanningTemplate,
			expectedTools: tools.DevOpsPlanningTools,
			state:         "planning",
		},
		{
			name:          "App Planning Template",
			storyType:     proto.StoryTypeApp,
			template:      templates.AppPlanningTemplate,
			expectedTools: tools.AppPlanningTools,
			state:         "planning",
		},
		{
			name:          "DevOps Coding Template",
			storyType:     proto.StoryTypeDevOps,
			template:      templates.DevOpsCodingTemplate,
			expectedTools: tools.DevOpsCodingTools,
			state:         "coding",
		},
		{
			name:          "App Coding Template",
			storyType:     proto.StoryTypeApp,
			template:      templates.AppCodingTemplate,
			expectedTools: tools.AppCodingTools,
			state:         "coding",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create AgentContext for the test
			agentCtx := tools.AgentContext{
				Executor:        exec.NewLocalExec(),
				ReadOnly:        tt.state == "planning",
				NetworkDisabled: true,
				WorkDir:         "/tmp/test",
			}

			// Create ToolProvider with the expected tools for this story type/state
			provider := tools.NewProvider(agentCtx, tt.expectedTools)

			// Generate tool documentation
			toolDoc := provider.GenerateToolDocumentation()

			// Verify tool documentation is not empty
			if toolDoc == "" {
				t.Errorf("Tool documentation should not be empty for %s", tt.name)
			}

			// Verify documentation contains all expected tools
			for _, expectedTool := range tt.expectedTools {
				if !strings.Contains(toolDoc, expectedTool) {
					t.Errorf("Tool documentation for %s should contain tool: %s", tt.name, expectedTool)
				}
			}

			// Verify documentation format
			if !strings.Contains(toolDoc, "## Available Tools") {
				t.Errorf("Tool documentation for %s should contain tools header", tt.name)
			}

			// Create template data with tool documentation
			templateData := &templates.TemplateData{
				TaskContent:       "Test task content",
				WorkDir:           "/tmp/test",
				ToolDocumentation: toolDoc,
				Extra: map[string]any{
					"story_type": string(tt.storyType),
				},
			}

			// Create a mock renderer to test template rendering
			renderer, err := templates.NewRenderer()
			if err != nil {
				t.Errorf("Failed to create renderer for %s: %v", tt.name, err)
				return
			}

			// Render the template with tool documentation
			renderedTemplate, err := renderer.Render(tt.template, templateData)
			if err != nil {
				t.Errorf("Failed to render template %s: %v", tt.name, err)
				return
			}

			// Verify the rendered template contains the tool documentation
			if !strings.Contains(renderedTemplate, toolDoc) {
				t.Errorf("Rendered template %s should contain tool documentation", tt.name)
			}

			// Verify the rendered template contains expected tools
			for _, expectedTool := range tt.expectedTools {
				if !strings.Contains(renderedTemplate, expectedTool) {
					t.Errorf("Rendered template %s should contain tool: %s", tt.name, expectedTool)
				}
			}

			// Story-type specific verifications
			switch tt.storyType {
			case proto.StoryTypeDevOps:
				// DevOps stories should include container tools in coding phase
				if tt.state == "coding" {
					if !strings.Contains(renderedTemplate, tools.ToolContainerBuild) {
						t.Errorf("DevOps coding template should contain container_build tool")
					}
				}
				// DevOps planning should include container_run for verification
				if tt.state == "planning" {
					if !strings.Contains(renderedTemplate, tools.ToolContainerRun) {
						t.Errorf("DevOps planning template should contain container_run tool")
					}
				}

			case proto.StoryTypeApp:
				// App coding should include build tools
				if tt.state == "coding" {
					appBuildTools := []string{tools.ToolBuild, tools.ToolTest, tools.ToolLint}
					for _, buildTool := range appBuildTools {
						if !strings.Contains(renderedTemplate, buildTool) {
							t.Errorf("App coding template should contain build tool: %s", buildTool)
						}
					}
				}
			}

			// Verify planning templates include planning-specific tools
			if tt.state == "planning" {
				planningTools := []string{tools.ToolSubmitPlan, tools.ToolAskQuestion, tools.ToolMarkStoryComplete}
				for _, planTool := range planningTools {
					if !strings.Contains(renderedTemplate, planTool) {
						t.Errorf("Planning template %s should contain tool: %s", tt.name, planTool)
					}
				}
			}
		})
	}
}

func TestCoderCreateToolProvider(t *testing.T) {
	// Create a mock coder for testing
	mockExecutor := exec.NewLocalExec()

	// Test planning tool provider creation
	t.Run("Planning ToolProvider Creation", func(t *testing.T) {
		// Mock agent context
		agentCtx := tools.AgentContext{
			Executor:        mockExecutor,
			ReadOnly:        true,
			NetworkDisabled: true,
			WorkDir:         "/tmp/test",
		}

		// Test DevOps planning tools
		devopsProvider := tools.NewProvider(agentCtx, tools.DevOpsPlanningTools)
		devopsTools := devopsProvider.List()

		expectedDevOpsCount := len(tools.DevOpsPlanningTools)
		if len(devopsTools) != expectedDevOpsCount {
			t.Errorf("DevOps planning provider should have %d tools, got %d", expectedDevOpsCount, len(devopsTools))
		}

		// Test App planning tools
		appProvider := tools.NewProvider(agentCtx, tools.AppPlanningTools)
		appTools := appProvider.List()

		expectedAppCount := len(tools.AppPlanningTools)
		if len(appTools) != expectedAppCount {
			t.Errorf("App planning provider should have %d tools, got %d", expectedAppCount, len(appTools))
		}
	})

	// Test coding tool provider creation
	t.Run("Coding ToolProvider Creation", func(t *testing.T) {
		// Mock agent context for coding (read-write)
		agentCtx := tools.AgentContext{
			Executor:        mockExecutor,
			ReadOnly:        false,
			NetworkDisabled: true,
			WorkDir:         "/tmp/test",
		}

		// Test DevOps coding tools
		devopsProvider := tools.NewProvider(agentCtx, tools.DevOpsCodingTools)
		devopsTools := devopsProvider.List()

		expectedDevOpsCount := len(tools.DevOpsCodingTools)
		if len(devopsTools) != expectedDevOpsCount {
			t.Errorf("DevOps coding provider should have %d tools, got %d", expectedDevOpsCount, len(devopsTools))
		}

		// Test App coding tools
		appProvider := tools.NewProvider(agentCtx, tools.AppCodingTools)
		appTools := appProvider.List()

		expectedAppCount := len(tools.AppCodingTools)
		if len(appTools) != expectedAppCount {
			t.Errorf("App coding provider should have %d tools, got %d", expectedAppCount, len(appTools))
		}
	})
}

func TestToolProviderIntegrationWithStoryTypes(t *testing.T) {
	// This test verifies that the integration between story types and tool providers works correctly
	testCases := []struct {
		storyType string
		state     string
		phase     string
	}{
		{"devops", "planning", "PLANNING"},
		{"app", "planning", "PLANNING"},
		{"devops", "coding", "CODING"},
		{"app", "coding", "CODING"},
	}

	for _, tc := range testCases {
		t.Run(tc.storyType+"_"+tc.state, func(t *testing.T) {
			// Create agent context
			agentCtx := tools.AgentContext{
				Executor:        exec.NewLocalExec(),
				ReadOnly:        tc.state == "planning",
				NetworkDisabled: true,
				WorkDir:         "/tmp/test",
			}

			// Select tools based on story type and state
			var expectedTools []string
			switch {
			case tc.storyType == "devops" && tc.state == "planning":
				expectedTools = tools.DevOpsPlanningTools
			case tc.storyType == "app" && tc.state == "planning":
				expectedTools = tools.AppPlanningTools
			case tc.storyType == "devops" && tc.state == "coding":
				expectedTools = tools.DevOpsCodingTools
			case tc.storyType == "app" && tc.state == "coding":
				expectedTools = tools.AppCodingTools
			}

			// Create provider
			provider := tools.NewProvider(agentCtx, expectedTools)

			// Verify tool count
			toolMetas := provider.List()
			if len(toolMetas) != len(expectedTools) {
				t.Errorf("Expected %d tools for %s %s, got %d", len(expectedTools), tc.storyType, tc.state, len(toolMetas))
			}

			// Verify all expected tools are available
			toolNames := make(map[string]bool)
			for _, meta := range toolMetas {
				toolNames[meta.Name] = true
			}

			for _, expectedTool := range expectedTools {
				if !toolNames[expectedTool] {
					t.Errorf("Missing expected tool %s for %s %s", expectedTool, tc.storyType, tc.state)
				}
			}

			// Generate and verify documentation
			doc := provider.GenerateToolDocumentation()
			if doc == "" {
				t.Errorf("Documentation should not be empty for %s %s", tc.storyType, tc.state)
			}

			// Verify documentation contains all tools
			for _, expectedTool := range expectedTools {
				if !strings.Contains(doc, expectedTool) {
					t.Errorf("Documentation should contain tool %s for %s %s", expectedTool, tc.storyType, tc.state)
				}
			}
		})
	}
}
