package tools

import (
	"strings"
	"testing"
)

func TestComposeUpTool_Definition(t *testing.T) {
	tool := NewComposeUpTool("/tmp", "test-agent", "")
	def := tool.Definition()

	if def.Name != "compose_up" {
		t.Errorf("expected name 'compose_up', got %q", def.Name)
	}

	if tool.Name() != "compose_up" {
		t.Errorf("expected Name() to return 'compose_up', got %q", tool.Name())
	}
}

func TestComposeDownTool_Definition(t *testing.T) {
	tool := NewComposeDownTool("/tmp")
	def := tool.Definition()

	if def.Name != "compose_down" {
		t.Errorf("expected name 'compose_down', got %q", def.Name)
	}

	if tool.Name() != "compose_down" {
		t.Errorf("expected Name() to return 'compose_down', got %q", tool.Name())
	}
}

func TestComposeLogsTool_Definition(t *testing.T) {
	tool := NewComposeLogsTool("/tmp")
	def := tool.Definition()

	if def.Name != "compose_logs" {
		t.Errorf("expected name 'compose_logs', got %q", def.Name)
	}

	if tool.Name() != "compose_logs" {
		t.Errorf("expected Name() to return 'compose_logs', got %q", tool.Name())
	}
}

func TestComposeStatusTool_Definition(t *testing.T) {
	tool := NewComposeStatusTool("/tmp")
	def := tool.Definition()

	if def.Name != "compose_status" {
		t.Errorf("expected name 'compose_status', got %q", def.Name)
	}

	if tool.Name() != "compose_status" {
		t.Errorf("expected Name() to return 'compose_status', got %q", tool.Name())
	}
}

func TestComposeTools_PromptDocumentation(t *testing.T) {
	tests := []struct {
		name     string
		tool     interface{ PromptDocumentation() string }
		contains string
	}{
		{"compose_up", NewComposeUpTool("/tmp", "test-agent", ""), "compose_up"},
		{"compose_down", NewComposeDownTool("/tmp"), "compose_down"},
		{"compose_logs", NewComposeLogsTool("/tmp"), "compose_logs"},
		{"compose_status", NewComposeStatusTool("/tmp"), "compose_status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := tt.tool.PromptDocumentation()
			if doc == "" {
				t.Error("expected non-empty documentation")
			}
		})
	}
}

func TestComposeUpTool_NoComposeFile(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewComposeUpTool(tmpDir, "test-agent", "")

	result, err := tool.Exec(t.Context(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Should mention no compose file found
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestComposeDownTool_NoComposeFile(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewComposeDownTool(tmpDir)

	result, err := tool.Exec(t.Context(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Should mention no compose file found
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestComposeLogsTool_NoComposeFile(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewComposeLogsTool(tmpDir)

	result, err := tool.Exec(t.Context(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Should mention no compose file found
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestComposeStatusTool_NoComposeFile(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewComposeStatusTool(tmpDir)

	result, err := tool.Exec(t.Context(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Should mention no compose file found
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestComposeUpTool_PathValidation(t *testing.T) {
	tests := []struct {
		name      string
		workDir   string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "valid absolute path",
			workDir:   "/workspace/project",
			wantError: false,
		},
		{
			name:      "relative path rejected",
			workDir:   "relative/path",
			wantError: true,
			errorMsg:  "workspace path must be absolute",
		},
		{
			name:      "path traversal in workDir",
			workDir:   "/workspace/../../../etc",
			wantError: false, // Clean path will normalize this, but compose file won't exist
		},
		{
			name:      "empty path rejected",
			workDir:   "",
			wantError: true,
			errorMsg:  "workspace path must be absolute",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewComposeUpTool(tt.workDir, "test-agent", "")
			err := tool.validateWorkspacePath()

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errorMsg)
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
