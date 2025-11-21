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

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected map result, got: %T", result)
	}

	success, ok := resultMap["success"].(bool)
	if !ok || !success {
		t.Errorf("Expected success=true, got: %v", resultMap)
	}

	// Verify metadata is present.
	metadata, ok := resultMap["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("Expected metadata map, got: %T", resultMap["metadata"])
	}

	if metadata["title"] != "Test Feature" {
		t.Errorf("Expected title 'Test Feature', got: %v", metadata["title"])
	}

	if metadata["requirements_count"] != 1 {
		t.Errorf("Expected 1 requirement, got: %v", metadata["requirements_count"])
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

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected map result, got: %T", result)
	}

	// After removing strict validation, even "invalid" specs are accepted.
	// The architect will provide feedback via the review process.
	success, ok := resultMap["success"].(bool)
	if !ok || !success {
		t.Errorf("Expected success=true (no strict validation), got: %v", resultMap)
	}

	// Verify metadata is present (even if incomplete).
	metadata, ok := resultMap["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("Expected metadata map, got: %T", resultMap["metadata"])
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
