package tools

import (
	"context"
	"testing"
)

const validSpecMarkdown = `---
version: "1.0"
priority: must
---

# Feature: Test Feature

## Vision
This is a test vision statement.

## Scope
### In Scope
- Feature A
- Feature B

### Out of Scope
- Feature C

## Requirements

### R-001: Test Requirement
**Type:** functional
**Priority:** must
**Dependencies:** []

**Description:** This is a test requirement description.

**Acceptance Criteria:**
- [ ] Criterion 1
- [ ] Criterion 2
`

const invalidSpecMarkdown = `---
version: "1.0"
---

# Feature: Incomplete Spec

This spec is missing required sections.
`

func TestSpecSubmitTool_ValidSpec(t *testing.T) {
	tool := NewSpecSubmitTool("")
	ctx := context.Background()

	args := map[string]any{
		"markdown": validSpecMarkdown,
		"summary":  "Test specification",
	}

	result, err := tool.Exec(ctx, args)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	// Check that ProcessEffect is present with correct signal
	if result.ProcessEffect == nil {
		t.Fatal("Expected ProcessEffect to be present")
	}

	if result.ProcessEffect.Signal != SignalSpecPreview {
		t.Errorf("Expected signal %s, got: %s", SignalSpecPreview, result.ProcessEffect.Signal)
	}

	// Check ProcessEffect.Data contains expected fields
	data, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatalf("Expected ProcessEffect.Data to be map[string]any, got: %T", result.ProcessEffect.Data)
	}

	// Verify user_spec is present
	userSpec, ok := data["user_spec"].(string)
	if !ok || userSpec == "" {
		t.Errorf("Expected user_spec in ProcessEffect.Data, got: %v", data)
	}

	// Verify infrastructure_spec is present (can be empty)
	_, ok = data["infrastructure_spec"].(string)
	if !ok {
		t.Errorf("Expected infrastructure_spec in ProcessEffect.Data, got: %v", data)
	}

	// Verify metadata is present
	metadata, ok := data["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("Expected metadata map, got: %T", data["metadata"])
	}

	if metadata["title"] != "Test Feature" {
		t.Errorf("Expected title 'Test Feature', got: %v", metadata["title"])
	}

	// Verify requirements count
	reqCount := metadata["requirements_count"]
	if reqCount != 1 {
		t.Errorf("Expected 1 requirement, got: %v (type: %T)", reqCount, reqCount)
	}
}

func TestSpecSubmitTool_InvalidSpec(t *testing.T) {
	tool := NewSpecSubmitTool("")
	ctx := context.Background()

	args := map[string]any{
		"markdown": invalidSpecMarkdown,
		"summary":  "Invalid spec",
	}

	result, err := tool.Exec(ctx, args)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	// Check that ProcessEffect is present with correct signal
	if result.ProcessEffect == nil {
		t.Fatal("Expected ProcessEffect to be present")
	}

	if result.ProcessEffect.Signal != SignalSpecPreview {
		t.Errorf("Expected signal %s, got: %s", SignalSpecPreview, result.ProcessEffect.Signal)
	}

	// Check ProcessEffect.Data contains expected fields
	data, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatalf("Expected ProcessEffect.Data to be map[string]any, got: %T", result.ProcessEffect.Data)
	}

	// Verify user_spec is present
	userSpec, ok := data["user_spec"].(string)
	if !ok || userSpec == "" {
		t.Errorf("Expected user_spec in ProcessEffect.Data, got: %v", data)
	}

	// Verify infrastructure_spec is present (can be empty)
	_, ok = data["infrastructure_spec"].(string)
	if !ok {
		t.Errorf("Expected infrastructure_spec in ProcessEffect.Data, got: %v", data)
	}

	// Verify metadata is present (even if incomplete).
	metadata, ok := data["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("Expected metadata map, got: %T", data["metadata"])
	}

	// Title should be parsed even from incomplete spec
	if metadata["title"] != "Incomplete Spec" {
		t.Errorf("Expected title 'Incomplete Spec', got: %v", metadata["title"])
	}
}

func TestSpecSubmitTool_MissingMarkdown(t *testing.T) {
	tool := NewSpecSubmitTool("")
	ctx := context.Background()

	args := map[string]any{
		"summary": "Test",
	}

	_, err := tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for missing markdown, got nil")
	}
}

func TestSpecSubmitTool_MissingSummary(t *testing.T) {
	tool := NewSpecSubmitTool("")
	ctx := context.Background()

	args := map[string]any{
		"markdown": validSpecMarkdown,
	}

	_, err := tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for missing summary, got nil")
	}
}

func TestSpecSubmitTool_EmptyMarkdown(t *testing.T) {
	tool := NewSpecSubmitTool("")
	ctx := context.Background()

	args := map[string]any{
		"markdown": "",
		"summary":  "Test",
	}

	_, err := tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for empty markdown, got nil")
	}
}

func TestSpecSubmitTool_Definition(t *testing.T) {
	tool := NewSpecSubmitTool("")
	def := tool.Definition()

	if def.Name != "spec_submit" {
		t.Errorf("Expected name 'spec_submit', got: %s", def.Name)
	}

	if def.Description == "" {
		t.Error("Expected non-empty description")
	}

	if len(def.InputSchema.Required) != 2 {
		t.Errorf("Expected 2 required fields, got: %d", len(def.InputSchema.Required))
	}
}
