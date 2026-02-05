package tools

import (
	"strings"
	"testing"
)

func TestGetDiffToolUsesConfiguredWorkspace(t *testing.T) {
	// Verify the tool stores and uses the workspace root
	workspace := "/mnt/coders/hotfix-001"
	tool := NewGetDiffTool(nil, workspace, 1000)

	// Check that the workspace root is stored correctly
	if tool.workspaceRoot != workspace {
		t.Errorf("expected workspaceRoot %q, got %q", workspace, tool.workspaceRoot)
	}
}

func TestGetDiffToolDefaultWorkspace(t *testing.T) {
	// Verify default workspace is set when empty string is passed
	tool := NewGetDiffTool(nil, "", 1000)

	if tool.workspaceRoot != "/workspace" {
		t.Errorf("expected default workspaceRoot '/workspace', got %q", tool.workspaceRoot)
	}
}

func TestGetDiffToolBuildDiffCommand(t *testing.T) {
	tool := NewGetDiffTool(nil, "/mnt/coders/coder-001", 1000)

	testCases := []struct {
		name    string
		path    string
		wantCmd string
		wantErr bool
	}{
		{
			name:    "no path - full diff",
			path:    "",
			wantCmd: "cd /mnt/coders/coder-001 && git diff --no-color --no-ext-diff origin/main 2>&1 | head -n 1000",
		},
		{
			name:    "specific file",
			path:    "db/questions.go",
			wantCmd: "cd /mnt/coders/coder-001 && git diff --no-color --no-ext-diff origin/main -- db/questions.go 2>&1 | head -n 1000",
		},
		{
			name:    "path traversal blocked",
			path:    "../../../etc/passwd",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := tool.buildDiffCommand(tool.workspaceRoot, tc.path)

			if tc.wantErr {
				if err == nil {
					t.Error("expected error for path traversal, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if cmd != tc.wantCmd {
				t.Errorf("expected command:\n%s\ngot:\n%s", tc.wantCmd, cmd)
			}
		})
	}
}

func TestGetDiffToolDefinitionHasNoRequiredParams(t *testing.T) {
	tool := NewGetDiffTool(nil, "/workspace", 1000)
	def := tool.Definition()

	// path should be optional (not in Required list)
	if len(def.InputSchema.Required) != 0 {
		t.Errorf("expected no required parameters, got %v", def.InputSchema.Required)
	}

	// path property should exist
	if _, exists := def.InputSchema.Properties["path"]; !exists {
		t.Error("expected 'path' property in schema")
	}

	// coder_id should NOT exist anymore
	if _, exists := def.InputSchema.Properties["coder_id"]; exists {
		t.Error("coder_id property should have been removed from schema")
	}
}

func TestGetDiffToolDocumentation(t *testing.T) {
	tool := NewGetDiffTool(nil, "/workspace", 1000)
	doc := tool.PromptDocumentation()

	// Should mention path parameter
	if !strings.Contains(doc, "path") {
		t.Error("documentation should mention path parameter")
	}

	// Should NOT mention coder_id anymore
	if strings.Contains(doc, "coder_id") {
		t.Error("documentation should not mention coder_id anymore")
	}
}
