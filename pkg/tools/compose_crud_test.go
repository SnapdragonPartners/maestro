package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createMaestroDir creates the .maestro directory in the test workspace.
func createMaestroDir(t *testing.T, tmpDir string) {
	t.Helper()
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("failed to create .maestro directory: %v", err)
	}
}

// composeFilePath returns the path to compose.yml in the test workspace.
func composeFilePath(tmpDir string) string {
	return filepath.Join(tmpDir, ".maestro", "compose.yml")
}

func TestComposeReadTool_Definition(t *testing.T) {
	tool := NewComposeReadTool("/tmp")
	def := tool.Definition()

	if def.Name != "compose_read" {
		t.Errorf("expected name 'compose_read', got %q", def.Name)
	}

	if tool.Name() != "compose_read" {
		t.Errorf("expected Name() to return 'compose_read', got %q", tool.Name())
	}
}

func TestComposeReadTool_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewComposeReadTool(tmpDir)

	result, err := tool.Exec(t.Context(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Content, "No compose.yml") {
		t.Errorf("expected message about missing file, got: %s", result.Content)
	}
}

func TestComposeReadTool_ReadsFile(t *testing.T) {
	tmpDir := t.TempDir()
	createMaestroDir(t, tmpDir)
	composePath := composeFilePath(tmpDir)
	content := "services:\n  app:\n    image: alpine\n"
	if err := os.WriteFile(composePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tool := NewComposeReadTool(tmpDir)

	result, err := tool.Exec(t.Context(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Content != content {
		t.Errorf("expected content %q, got %q", content, result.Content)
	}
}

func TestComposeWriteTool_Definition(t *testing.T) {
	tool := NewComposeWriteTool("/tmp")
	def := tool.Definition()

	if def.Name != "compose_write" {
		t.Errorf("expected name 'compose_write', got %q", def.Name)
	}

	if tool.Name() != "compose_write" {
		t.Errorf("expected Name() to return 'compose_write', got %q", tool.Name())
	}
}

func TestComposeWriteTool_MissingContent(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewComposeWriteTool(tmpDir)

	result, err := tool.Exec(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Content, "Error") {
		t.Error("expected error for missing content")
	}
}

func TestComposeWriteTool_WritesFile(t *testing.T) {
	tmpDir := t.TempDir()
	createMaestroDir(t, tmpDir)
	tool := NewComposeWriteTool(tmpDir)

	content := "services:\n  app:\n    image: nginx\n"
	result, err := tool.Exec(t.Context(), map[string]any{
		"content": content,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result.Content, "Error") {
		t.Errorf("unexpected error: %s", result.Content)
	}

	// Verify file was written
	composePath := composeFilePath(tmpDir)
	written, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}

	if string(written) != content {
		t.Errorf("file content mismatch: expected %q, got %q", content, string(written))
	}
}

func TestComposeValidateTool_Definition(t *testing.T) {
	tool := NewComposeValidateTool("/tmp")
	def := tool.Definition()

	if def.Name != "compose_validate" {
		t.Errorf("expected name 'compose_validate', got %q", def.Name)
	}

	if tool.Name() != "compose_validate" {
		t.Errorf("expected Name() to return 'compose_validate', got %q", tool.Name())
	}
}

func TestComposeValidateTool_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewComposeValidateTool(tmpDir)

	result, err := tool.Exec(t.Context(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Content, "No compose.yml") {
		t.Error("expected error for missing file")
	}
}

func TestComposeAddServiceTool_Definition(t *testing.T) {
	tool := NewComposeAddServiceTool("/tmp")
	def := tool.Definition()

	if def.Name != "compose_add_service" {
		t.Errorf("expected name 'compose_add_service', got %q", def.Name)
	}

	if tool.Name() != "compose_add_service" {
		t.Errorf("expected Name() to return 'compose_add_service', got %q", tool.Name())
	}
}

func TestComposeAddServiceTool_MissingName(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewComposeAddServiceTool(tmpDir)

	result, err := tool.Exec(t.Context(), map[string]any{
		"image": "alpine",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Content, "Error") {
		t.Error("expected error for missing name")
	}
}

func TestComposeAddServiceTool_MissingImage(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewComposeAddServiceTool(tmpDir)

	result, err := tool.Exec(t.Context(), map[string]any{
		"name": "app",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Content, "Error") {
		t.Error("expected error for missing image")
	}
}

func TestComposeAddServiceTool_CreatesNewFile(t *testing.T) {
	tmpDir := t.TempDir()
	createMaestroDir(t, tmpDir)
	tool := NewComposeAddServiceTool(tmpDir)

	result, err := tool.Exec(t.Context(), map[string]any{
		"name":  "db",
		"image": "postgres:16",
		"ports": []any{"5432:5432"},
		"environment": map[string]any{
			"POSTGRES_PASSWORD": "secret",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result.Content, "Error") {
		t.Errorf("unexpected error: %s", result.Content)
	}

	// Verify file was created
	composePath := composeFilePath(tmpDir)
	content, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("failed to read compose file: %v", err)
	}

	if !strings.Contains(string(content), "db:") {
		t.Errorf("expected 'db:' in compose file, got: %s", string(content))
	}
	if !strings.Contains(string(content), "postgres:16") {
		t.Errorf("expected 'postgres:16' in compose file, got: %s", string(content))
	}
}

func TestComposeAddServiceTool_AddsToExisting(t *testing.T) {
	tmpDir := t.TempDir()
	createMaestroDir(t, tmpDir)
	composePath := composeFilePath(tmpDir)

	// Create initial compose file
	initial := "services:\n  app:\n    image: alpine\n"
	if err := os.WriteFile(composePath, []byte(initial), 0644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	tool := NewComposeAddServiceTool(tmpDir)

	result, err := tool.Exec(t.Context(), map[string]any{
		"name":  "redis",
		"image": "redis:7-alpine",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result.Content, "Error") {
		t.Errorf("unexpected error: %s", result.Content)
	}

	// Verify both services exist
	content, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("failed to read compose file: %v", err)
	}

	if !strings.Contains(string(content), "app:") {
		t.Errorf("expected 'app:' to remain in compose file")
	}
	if !strings.Contains(string(content), "redis:") {
		t.Errorf("expected 'redis:' in compose file")
	}
}

func TestComposeRemoveServiceTool_Definition(t *testing.T) {
	tool := NewComposeRemoveServiceTool("/tmp")
	def := tool.Definition()

	if def.Name != "compose_remove_service" {
		t.Errorf("expected name 'compose_remove_service', got %q", def.Name)
	}

	if tool.Name() != "compose_remove_service" {
		t.Errorf("expected Name() to return 'compose_remove_service', got %q", tool.Name())
	}
}

func TestComposeRemoveServiceTool_MissingName(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewComposeRemoveServiceTool(tmpDir)

	result, err := tool.Exec(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Content, "Error") {
		t.Error("expected error for missing name")
	}
}

func TestComposeRemoveServiceTool_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewComposeRemoveServiceTool(tmpDir)

	result, err := tool.Exec(t.Context(), map[string]any{
		"name": "app",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.Content, "No compose.yml") {
		t.Error("expected error for missing file")
	}
}

func TestComposeRemoveServiceTool_ServiceNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	createMaestroDir(t, tmpDir)
	composePath := composeFilePath(tmpDir)

	initial := "services:\n  app:\n    image: alpine\n"
	if err := os.WriteFile(composePath, []byte(initial), 0644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	tool := NewComposeRemoveServiceTool(tmpDir)

	result, err := tool.Exec(t.Context(), map[string]any{
		"name": "nonexistent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should succeed but indicate service not found
	if !strings.Contains(result.Content, "not found") {
		t.Errorf("expected 'not found' message, got: %s", result.Content)
	}
}

func TestComposeRemoveServiceTool_RemovesService(t *testing.T) {
	tmpDir := t.TempDir()
	createMaestroDir(t, tmpDir)
	composePath := composeFilePath(tmpDir)

	initial := "services:\n  app:\n    image: alpine\n  db:\n    image: postgres\n"
	if err := os.WriteFile(composePath, []byte(initial), 0644); err != nil {
		t.Fatalf("failed to write initial file: %v", err)
	}

	tool := NewComposeRemoveServiceTool(tmpDir)

	result, err := tool.Exec(t.Context(), map[string]any{
		"name": "db",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result.Content, "Error") {
		t.Errorf("unexpected error: %s", result.Content)
	}

	// Verify db was removed but app remains
	content, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("failed to read compose file: %v", err)
	}

	if !strings.Contains(string(content), "app:") {
		t.Errorf("expected 'app:' to remain in compose file")
	}
	if strings.Contains(string(content), "db:") {
		t.Errorf("expected 'db:' to be removed from compose file")
	}
}

func TestComposeCRUDTools_PromptDocumentation(t *testing.T) {
	tests := []struct {
		name string
		tool interface{ PromptDocumentation() string }
	}{
		{"compose_read", NewComposeReadTool("/tmp")},
		{"compose_write", NewComposeWriteTool("/tmp")},
		{"compose_validate", NewComposeValidateTool("/tmp")},
		{"compose_add_service", NewComposeAddServiceTool("/tmp")},
		{"compose_remove_service", NewComposeRemoveServiceTool("/tmp")},
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
