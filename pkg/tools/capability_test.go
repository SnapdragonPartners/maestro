package tools

import (
	"strings"
	"testing"

	"orchestrator/pkg/exec"
	"orchestrator/pkg/proto"
)

func TestToolProviderDevOpsPlanningTools(t *testing.T) {
	// Create AgentContext for DevOps planning
	agentCtx := AgentContext{
		Executor:        exec.NewLocalExec(),
		ReadOnly:        true,
		NetworkDisabled: true,
		WorkDir:         "/tmp",
	}

	// Create provider with DevOps planning tools
	provider := NewProvider(agentCtx, DevOpsPlanningTools)

	// Get available tool metadata
	toolMetas := provider.List()

	// Verify expected number of tools
	expectedCount := len(DevOpsPlanningTools)
	if len(toolMetas) != expectedCount {
		t.Errorf("Expected %d DevOps planning tools, got %d", expectedCount, len(toolMetas))
	}

	// Verify all expected tools are present
	expectedTools := map[string]bool{
		ToolShell:             false,
		ToolSubmitPlan:        false,
		ToolAskQuestion:       false,
		ToolMarkStoryComplete: false,
		ToolContainerTest:     false,
		ToolContainerList:     false,
		ToolChatPost:          false,
		ToolChatRead:          false,
	}

	for _, meta := range toolMetas {
		if _, exists := expectedTools[meta.Name]; exists {
			expectedTools[meta.Name] = true
		} else {
			t.Errorf("Unexpected tool in DevOps planning: %s", meta.Name)
		}
	}

	// Check for missing tools
	for toolName, found := range expectedTools {
		if !found {
			t.Errorf("Missing expected DevOps planning tool: %s", toolName)
		}
	}
}

func TestToolProviderAppPlanningTools(t *testing.T) {
	// Create AgentContext for App planning
	agentCtx := AgentContext{
		Executor:        exec.NewLocalExec(),
		ReadOnly:        true,
		NetworkDisabled: true,
		WorkDir:         "/tmp",
	}

	// Create provider with App planning tools
	provider := NewProvider(agentCtx, AppPlanningTools)

	// Get available tool metadata
	toolMetas := provider.List()

	// Verify expected number of tools
	expectedCount := len(AppPlanningTools)
	if len(toolMetas) != expectedCount {
		t.Errorf("Expected %d App planning tools, got %d", expectedCount, len(toolMetas))
	}

	// Verify all expected tools are present
	expectedTools := map[string]bool{
		ToolShell:             false,
		ToolSubmitPlan:        false,
		ToolAskQuestion:       false,
		ToolMarkStoryComplete: false,
		ToolChatPost:          false,
		ToolChatRead:          false,
	}

	for _, meta := range toolMetas {
		if _, exists := expectedTools[meta.Name]; exists {
			expectedTools[meta.Name] = true
		} else {
			t.Errorf("Unexpected tool in App planning: %s", meta.Name)
		}
	}

	// Check for missing tools
	for toolName, found := range expectedTools {
		if !found {
			t.Errorf("Missing expected App planning tool: %s", toolName)
		}
	}
}

func TestToolProviderAppCodingTools(t *testing.T) {
	// Create AgentContext for App coding
	agentCtx := AgentContext{
		Executor:        exec.NewLocalExec(),
		ReadOnly:        false, // Coding allows writes
		NetworkDisabled: true,
		WorkDir:         "/tmp",
	}

	// Create provider with App coding tools
	provider := NewProvider(agentCtx, AppCodingTools)

	// Get available tool metadata
	toolMetas := provider.List()

	// Verify expected number of tools
	expectedCount := len(AppCodingTools)
	if len(toolMetas) != expectedCount {
		t.Errorf("Expected %d App coding tools, got %d", expectedCount, len(toolMetas))
	}

	// Verify all expected tools are present
	expectedTools := map[string]bool{
		ToolShell:        false,
		ToolBuild:        false,
		ToolTest:         false,
		ToolLint:         false,
		ToolAskQuestion:  false,
		ToolDone:         false,
		ToolChatPost:     false,
		ToolChatRead:     false,
		ToolTodosAdd:     false,
		ToolTodoComplete: false,
		ToolTodoUpdate:   false,
	}

	for _, meta := range toolMetas {
		if _, exists := expectedTools[meta.Name]; exists {
			expectedTools[meta.Name] = true
		} else {
			t.Errorf("Unexpected tool in App coding: %s", meta.Name)
		}
	}

	// Check for missing tools
	for toolName, found := range expectedTools {
		if !found {
			t.Errorf("Missing expected App coding tool: %s", toolName)
		}
	}
}

func TestToolProviderGenerateDocumentation(t *testing.T) {
	// Create AgentContext
	agentCtx := AgentContext{
		Executor:        exec.NewLocalExec(),
		ReadOnly:        true,
		NetworkDisabled: true,
		WorkDir:         "/tmp",
	}

	// Test DevOps planning documentation
	provider := NewProvider(agentCtx, DevOpsPlanningTools)
	doc := provider.GenerateToolDocumentation()

	// Verify documentation is generated
	if doc == "" {
		t.Error("Generated documentation should not be empty")
	}

	// Verify documentation contains expected tools
	expectedTools := []string{ToolShell, ToolSubmitPlan, ToolAskQuestion, ToolMarkStoryComplete, ToolContainerTest, ToolContainerList, ToolChatPost, ToolChatRead}
	for _, toolName := range expectedTools {
		if !strings.Contains(doc, toolName) {
			t.Errorf("Documentation should contain tool: %s", toolName)
		}
	}

	// Verify documentation format
	if !strings.Contains(doc, "## Available Tools") {
		t.Error("Documentation should contain tools header")
	}
}

func TestToolProviderCanRetrieveTools(t *testing.T) {
	// Create AgentContext
	agentCtx := AgentContext{
		Executor:        exec.NewLocalExec(),
		ReadOnly:        true,
		NetworkDisabled: true,
		WorkDir:         "/tmp",
	}

	// Create provider
	provider := NewProvider(agentCtx, []string{ToolShell, ToolSubmitPlan})

	// Test retrieving existing tool
	shellTool, err := provider.Get(ToolShell)
	if err != nil {
		t.Errorf("Should be able to get shell tool: %v", err)
	}
	if shellTool == nil {
		t.Error("Shell tool should not be nil")
	}
	if shellTool.Name() != ToolShell {
		t.Errorf("Expected tool name %s, got %s", ToolShell, shellTool.Name())
	}

	// Test retrieving planning tool
	planTool, err := provider.Get(ToolSubmitPlan)
	if err != nil {
		t.Errorf("Should be able to get submit_plan tool: %v", err)
	}
	if planTool == nil {
		t.Error("Submit plan tool should not be nil")
	}

	// Test retrieving disallowed tool
	_, err = provider.Get(ToolBuild)
	if err == nil {
		t.Error("Should not be able to get disallowed tool")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("Error should mention tool not allowed, got: %v", err)
	}
}

func TestStoryTypeToolMapping(t *testing.T) {
	//nolint:govet // fieldalignment: Test struct alignment is not performance critical
	tests := []struct {
		name          string
		storyType     proto.StoryType
		expectedTools []string
		state         string
	}{
		{
			name:          "DevOps Planning",
			storyType:     proto.StoryTypeDevOps,
			expectedTools: DevOpsPlanningTools,
			state:         "planning",
		},
		{
			name:          "App Planning",
			storyType:     proto.StoryTypeApp,
			expectedTools: AppPlanningTools,
			state:         "planning",
		},
		{
			name:          "DevOps Coding",
			storyType:     proto.StoryTypeDevOps,
			expectedTools: DevOpsCodingTools,
			state:         "coding",
		},
		{
			name:          "App Coding",
			storyType:     proto.StoryTypeApp,
			expectedTools: AppCodingTools,
			state:         "coding",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentCtx := AgentContext{
				Executor:        exec.NewLocalExec(),
				ReadOnly:        tt.state == "planning",
				NetworkDisabled: true,
				WorkDir:         "/tmp",
			}

			provider := NewProvider(agentCtx, tt.expectedTools)
			toolMetas := provider.List()

			if len(toolMetas) != len(tt.expectedTools) {
				t.Errorf("Expected %d tools for %s, got %d", len(tt.expectedTools), tt.name, len(toolMetas))
			}

			// Verify all expected tools are present
			expectedToolSet := make(map[string]bool)
			for _, tool := range tt.expectedTools {
				expectedToolSet[tool] = false
			}

			for _, meta := range toolMetas {
				if _, exists := expectedToolSet[meta.Name]; exists {
					expectedToolSet[meta.Name] = true
				}
			}

			for toolName, found := range expectedToolSet {
				if !found {
					t.Errorf("Missing tool %s in %s", toolName, tt.name)
				}
			}
		})
	}
}
