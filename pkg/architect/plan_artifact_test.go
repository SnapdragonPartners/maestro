package architect

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

// TestSaveApprovedPlanArtifact tests the plan artifact saving functionality.
func TestSaveApprovedPlanArtifact(t *testing.T) {
	// Create temp directory for test.
	tempDir, err := os.MkdirTemp("", "plan-artifact-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create state store.
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create architect driver.
	driver := NewDriver("test-architect", stateStore, &config.ModelCfg{}, nil, nil, tempDir, "", nil)

	ctx := context.Background()

	// Create test approval request message.
	requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "test-coder", "test-architect")
	requestMsg.SetPayload("request_type", proto.RequestApproval.String())
	requestMsg.SetPayload("approval_type", proto.ApprovalTypePlan.String())
	requestMsg.SetPayload("content", "Test implementation plan for JWT authentication system")
	requestMsg.SetPayload("confidence", "HIGH")
	requestMsg.SetPayload("exploration_summary", "Explored 10 files, found existing auth patterns")
	requestMsg.SetPayload("risks", "Potential breaking changes to existing session auth")
	requestMsg.SetPayload("approval_id", "test-approval-123")

	// Test saving plan artifact.
	err = driver.saveApprovedPlanArtifact(ctx, requestMsg, "Test plan content")
	if err != nil {
		t.Fatalf("Failed to save plan artifact: %v", err)
	}

	// Verify stories/plans directory was created.
	storiesDir := filepath.Join(tempDir, "stories", "plans")
	if _, err := os.Stat(storiesDir); os.IsNotExist(err) {
		t.Error("Expected stories/plans directory to be created")
	}

	// Verify artifact file was created.
	files, err := os.ReadDir(storiesDir)
	if err != nil {
		t.Fatalf("Failed to read stories/plans directory: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("Expected 1 artifact file, found %d", len(files))
	}

	// Verify file has correct naming pattern.
	filename := files[0].Name()
	expectedPrefix := "approved-plan-test-coder-"
	if !strings.HasPrefix(filename, expectedPrefix) {
		t.Errorf("Expected filename to start with %s, got %s", expectedPrefix, filename)
	}

	if !strings.HasSuffix(filename, ".json") {
		t.Errorf("Expected filename to end with .json, got %s", filename)
	}

	// Read and verify artifact content.
	artifactPath := filepath.Join(storiesDir, filename)
	content, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("Failed to read artifact file: %v", err)
	}

	// Parse JSON content.
	var artifact map[string]interface{}
	if err := json.Unmarshal(content, &artifact); err != nil {
		t.Fatalf("Failed to parse artifact JSON: %v", err)
	}

	// Verify artifact structure and content.
	expectedFields := []string{"timestamp", "architect_id", "agent_id", "approval_id", "message", "plan_content", "confidence", "exploration_summary", "risks"}
	for _, field := range expectedFields {
		if _, exists := artifact[field]; !exists {
			t.Errorf("Expected artifact field %s not found", field)
		}
	}

	// Verify specific values.
	if artifact["architect_id"] != "test-architect" {
		t.Errorf("Expected architect_id 'test-architect', got %v", artifact["architect_id"])
	}

	if artifact["agent_id"] != "test-coder" {
		t.Errorf("Expected agent_id 'test-coder', got %v", artifact["agent_id"])
	}

	if artifact["approval_id"] != "test-approval-123" {
		t.Errorf("Expected approval_id 'test-approval-123', got %v", artifact["approval_id"])
	}

	if artifact["confidence"] != "HIGH" {
		t.Errorf("Expected confidence 'HIGH', got %v", artifact["confidence"])
	}

	if artifact["plan_content"] != "Test plan content" {
		t.Errorf("Expected plan_content 'Test plan content', got %v", artifact["plan_content"])
	}

	t.Log("Plan artifact saving functionality works correctly")
}

// TestPlanArtifactWithMissingFields tests plan artifact saving with missing optional fields.
func TestPlanArtifactWithMissingFields(t *testing.T) {
	// Create temp directory for test.
	tempDir, err := os.MkdirTemp("", "plan-artifact-missing-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create state store.
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create architect driver.
	driver := NewDriver("test-architect", stateStore, &config.ModelCfg{}, nil, nil, tempDir, "", nil)

	ctx := context.Background()

	// Create test request with minimal fields.
	requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "test-coder", "test-architect")
	requestMsg.SetPayload("request_type", proto.RequestApproval.String())
	requestMsg.SetPayload("approval_type", proto.ApprovalTypePlan.String())
	requestMsg.SetPayload("content", "Minimal plan content")
	requestMsg.SetPayload("approval_id", "test-approval-456")
	// Note: missing confidence, exploration_summary, risks.

	// Should still save successfully with empty strings for missing fields.
	err = driver.saveApprovedPlanArtifact(ctx, requestMsg, "Minimal plan")
	if err != nil {
		t.Fatalf("Failed to save plan artifact with missing fields: %v", err)
	}

	// Verify artifact was created.
	storiesDir := filepath.Join(tempDir, "stories", "plans")
	files, err := os.ReadDir(storiesDir)
	if err != nil {
		t.Fatalf("Failed to read stories/plans directory: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("Expected 1 artifact file, found %d", len(files))
	}

	// Read and verify content.
	artifactPath := filepath.Join(storiesDir, files[0].Name())
	content, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("Failed to read artifact file: %v", err)
	}

	var artifact map[string]interface{}
	if err := json.Unmarshal(content, &artifact); err != nil {
		t.Fatalf("Failed to parse artifact JSON: %v", err)
	}

	// Verify missing fields are empty strings.
	if artifact["confidence"] != "" {
		t.Errorf("Expected empty confidence, got %v", artifact["confidence"])
	}

	if artifact["exploration_summary"] != "" {
		t.Errorf("Expected empty exploration_summary, got %v", artifact["exploration_summary"])
	}

	if artifact["risks"] != "" {
		t.Errorf("Expected empty risks, got %v", artifact["risks"])
	}

	t.Log("Plan artifact saving with missing fields works correctly")
}
