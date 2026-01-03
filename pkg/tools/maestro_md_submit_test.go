package tools

import (
	"context"
	"strings"
	"testing"

	"orchestrator/pkg/utils"
)

func TestMaestroMdSubmitTool_Name(t *testing.T) {
	tool := NewMaestroMdSubmitTool()
	if tool.Name() != ToolMaestroMdSubmit {
		t.Errorf("Expected name %q, got %q", ToolMaestroMdSubmit, tool.Name())
	}
}

func TestMaestroMdSubmitTool_Definition(t *testing.T) {
	tool := NewMaestroMdSubmitTool()
	def := tool.Definition()

	if def.Name != ToolMaestroMdSubmit {
		t.Errorf("Expected name %q, got %q", ToolMaestroMdSubmit, def.Name)
	}

	if def.Description == "" {
		t.Error("Expected non-empty description")
	}

	// Check required content parameter
	contentProp, ok := def.InputSchema.Properties["content"]
	if !ok {
		t.Fatal("Expected content property in schema")
	}

	if contentProp.Type != "string" {
		t.Errorf("Expected content type string, got %q", contentProp.Type)
	}

	// Check content is required
	foundRequired := false
	for _, req := range def.InputSchema.Required {
		if req == "content" {
			foundRequired = true
			break
		}
	}
	if !foundRequired {
		t.Error("Expected content to be required")
	}
}

func TestMaestroMdSubmitTool_PromptDocumentation(t *testing.T) {
	tool := NewMaestroMdSubmitTool()
	doc := tool.PromptDocumentation()

	if !strings.Contains(doc, "maestro_md_submit") {
		t.Error("Expected tool name in documentation")
	}
	if !strings.Contains(doc, "content") {
		t.Error("Expected content parameter in documentation")
	}
}

func TestMaestroMdSubmitTool_Exec_Success(t *testing.T) {
	tool := NewMaestroMdSubmitTool()
	ctx := context.Background()

	testContent := "# Test Project\n\nThis is a test project."
	args := map[string]any{
		"content": testContent,
	}

	result, err := tool.Exec(ctx, args)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}

	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	if result.ProcessEffect == nil {
		t.Fatal("Expected ProcessEffect")
	}

	if result.ProcessEffect.Signal != SignalMaestroMdComplete {
		t.Errorf("Expected signal %q, got %q", SignalMaestroMdComplete, result.ProcessEffect.Signal)
	}

	data, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatal("Expected Data to be map[string]any")
	}

	if data["content"] != testContent {
		t.Errorf("Expected content %q in data, got %q", testContent, data["content"])
	}
}

func TestMaestroMdSubmitTool_Exec_EmptyContent(t *testing.T) {
	tool := NewMaestroMdSubmitTool()
	ctx := context.Background()

	args := map[string]any{
		"content": "",
	}

	_, err := tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for empty content")
	}

	if !strings.Contains(err.Error(), "required") {
		t.Errorf("Expected 'required' in error message, got: %v", err)
	}
}

func TestMaestroMdSubmitTool_Exec_MissingContent(t *testing.T) {
	tool := NewMaestroMdSubmitTool()
	ctx := context.Background()

	args := map[string]any{}

	_, err := tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for missing content")
	}
}

func TestMaestroMdSubmitTool_Exec_ContentTooLong(t *testing.T) {
	tool := NewMaestroMdSubmitTool()
	ctx := context.Background()

	// Create content that exceeds the limit
	longContent := strings.Repeat("x", utils.MaestroMdCharLimit+1)
	args := map[string]any{
		"content": longContent,
	}

	_, err := tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for content exceeding limit")
	}

	if !strings.Contains(err.Error(), "exceeds maximum length") {
		t.Errorf("Expected 'exceeds maximum length' in error message, got: %v", err)
	}
}

func TestMaestroMdSubmitTool_Exec_ContentAtLimit(t *testing.T) {
	tool := NewMaestroMdSubmitTool()
	ctx := context.Background()

	// Create content exactly at the limit
	atLimitContent := strings.Repeat("x", utils.MaestroMdCharLimit)
	args := map[string]any{
		"content": atLimitContent,
	}

	result, err := tool.Exec(ctx, args)
	if err != nil {
		t.Fatalf("Exec failed for content at limit: %v", err)
	}

	if result == nil {
		t.Fatal("Expected non-nil result for content at limit")
	}
}
