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
		baseSHA string
		wantCmd string
		wantErr bool
	}{
		{
			name:    "no path, no base - uses merge-base SHA",
			path:    "",
			baseSHA: "abc123",
			wantCmd: "cd /mnt/coders/coder-001 && git diff --no-color --no-ext-diff abc123..HEAD 2>&1 | head -n 1000",
		},
		{
			name:    "specific file with base SHA",
			path:    "db/questions.go",
			baseSHA: "abc123",
			wantCmd: "cd /mnt/coders/coder-001 && git diff --no-color --no-ext-diff abc123..HEAD -- db/questions.go 2>&1 | head -n 1000",
		},
		{
			name:    "empty base SHA falls back to origin/main",
			path:    "",
			baseSHA: "",
			wantCmd: "cd /mnt/coders/coder-001 && git diff --no-color --no-ext-diff origin/main 2>&1 | head -n 1000",
		},
		{
			name:    "specific file with empty base SHA",
			path:    "main.go",
			baseSHA: "",
			wantCmd: "cd /mnt/coders/coder-001 && git diff --no-color --no-ext-diff origin/main -- main.go 2>&1 | head -n 1000",
		},
		{
			name:    "path traversal blocked",
			path:    "../../../etc/passwd",
			baseSHA: "abc123",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := tool.buildDiffCommand(tool.workspaceRoot, tc.path, tc.baseSHA)

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

	// No required parameters
	if len(def.InputSchema.Required) != 0 {
		t.Errorf("expected no required parameters, got %v", def.InputSchema.Required)
	}

	// path property should exist
	if _, exists := def.InputSchema.Properties["path"]; !exists {
		t.Error("expected 'path' property in schema")
	}

	// base property should exist
	if _, exists := def.InputSchema.Properties["base"]; !exists {
		t.Error("expected 'base' property in schema")
	}

	// coder_id should NOT exist
	if _, exists := def.InputSchema.Properties["coder_id"]; exists {
		t.Error("coder_id property should have been removed from schema")
	}
}

func TestGetDiffToolDefinitionDescribesMergeBase(t *testing.T) {
	tool := NewGetDiffTool(nil, "/workspace", 1000)
	def := tool.Definition()

	// Description should mention merge-base behavior
	if !strings.Contains(def.Description, "merge-base") {
		t.Error("tool description should mention merge-base default behavior")
	}

	// Base property description should explain merge-base default
	baseProp, exists := def.InputSchema.Properties["base"]
	if !exists {
		t.Fatal("expected 'base' property in schema")
	}
	if !strings.Contains(baseProp.Description, "merge-base") {
		t.Error("base property description should mention merge-base")
	}
	if !strings.Contains(baseProp.Description, "origin/main") {
		t.Error("base property description should mention origin/main")
	}
}

func TestGetDiffToolDocumentation(t *testing.T) {
	tool := NewGetDiffTool(nil, "/workspace", 1000)
	doc := tool.PromptDocumentation()

	// Should mention path parameter
	if !strings.Contains(doc, "path") {
		t.Error("documentation should mention path parameter")
	}

	// Should mention base parameter
	if !strings.Contains(doc, "base") {
		t.Error("documentation should mention base parameter")
	}

	// Should mention merge-base behavior
	if !strings.Contains(doc, "merge-base") {
		t.Error("documentation should mention merge-base default")
	}

	// Should NOT mention coder_id
	if strings.Contains(doc, "coder_id") {
		t.Error("documentation should not mention coder_id")
	}

	// Should mention traceability fields
	if !strings.Contains(doc, "head_sha") {
		t.Error("documentation should mention head_sha")
	}
	if !strings.Contains(doc, "base_sha") {
		t.Error("documentation should mention base_sha")
	}
}
