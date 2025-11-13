package main

import (
	"context"
	"fmt"
	"os"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
)

// TestNewBootstrapFlow tests bootstrap flow creation.
func TestNewBootstrapFlow(t *testing.T) {
	gitRepo := "https://github.com/test/repo.git"
	specFile := "/path/to/spec.md"

	flow := NewBootstrapFlow(gitRepo, specFile)

	if flow == nil {
		t.Fatal("NewBootstrapFlow returned nil")
	}

	if flow.gitRepo != gitRepo {
		t.Errorf("Expected gitRepo to be %s, got %s", gitRepo, flow.gitRepo)
	}

	if flow.specFile != specFile {
		t.Errorf("Expected specFile to be %s, got %s", specFile, flow.specFile)
	}
}

// TestNewMainFlow tests main flow creation.
func TestNewMainFlow(t *testing.T) {
	specFile := "/path/to/spec.md"
	webUI := true

	flow := NewMainFlow(specFile, webUI)

	if flow == nil {
		t.Fatal("NewMainFlow returned nil")
	}

	if flow.specFile != specFile {
		t.Errorf("Expected specFile to be %s, got %s", specFile, flow.specFile)
	}

	if flow.webUI != webUI {
		t.Errorf("Expected webUI to be %t, got %t", webUI, flow.webUI)
	}
}

// TestInjectSpecMessage tests spec message creation.
func TestInjectSpecMessage(t *testing.T) {
	// This test verifies the message structure without actually sending it
	// We'll test the message construction logic that InjectSpec uses

	source := "test-source"
	content := []byte("# Test Specification\n\nThis is a test spec.")

	// Simulate the message creation logic from InjectSpec (now uses REQUEST)
	msg := proto.NewAgentMsg(proto.MsgTypeREQUEST, source, string(agent.TypeArchitect))

	// Build approval request payload (unified with PM flow)
	approvalPayload := &proto.ApprovalRequestPayload{
		ApprovalType: proto.ApprovalTypeSpec,
		Content:      string(content),
		Reason:       fmt.Sprintf("Spec submitted via %s", source),
		Metadata:     make(map[string]string),
	}
	approvalPayload.Metadata["source"] = source

	msg.SetTypedPayload(proto.NewApprovalRequestPayload(approvalPayload))
	msg.SetMetadata("approval_id", proto.GenerateApprovalID())
	msg.SetMetadata("source", source)

	// Verify message structure
	if msg.Type != proto.MsgTypeREQUEST {
		t.Errorf("Expected message type to be %s, got %s", proto.MsgTypeREQUEST, msg.Type)
	}

	if msg.FromAgent != source {
		t.Errorf("Expected message from to be %s, got %s", source, msg.FromAgent)
	}

	if msg.ToAgent != string(agent.TypeArchitect) {
		t.Errorf("Expected message to to be %s, got %s", string(agent.TypeArchitect), msg.ToAgent)
	}

	// Check typed payload - should be ApprovalRequestPayload
	typedPayload := msg.GetTypedPayload()
	if typedPayload == nil {
		t.Fatal("Expected typed payload to exist")
	}

	approvalReq, err := typedPayload.ExtractApprovalRequest()
	if err != nil {
		t.Fatalf("Failed to extract approval request payload: %v", err)
	}

	if approvalReq.ApprovalType != proto.ApprovalTypeSpec {
		t.Errorf("Expected approval type to be %s, got %s", proto.ApprovalTypeSpec, approvalReq.ApprovalType)
	}

	if approvalReq.Content != string(content) {
		t.Errorf("Expected content to be %s, got %s", string(content), approvalReq.Content)
	}

	expectedReason := fmt.Sprintf("Spec submitted via %s", source)
	if approvalReq.Reason != expectedReason {
		t.Errorf("Expected reason to be %s, got %s", expectedReason, approvalReq.Reason)
	}

	if approvalReq.Metadata["source"] != source {
		t.Errorf("Expected metadata source to be %s, got %s", source, approvalReq.Metadata["source"])
	}

	// Check metadata
	metadataSource, exists := msg.GetMetadata("source")
	if !exists {
		t.Error("Expected metadata source to exist")
	}
	if metadataSource != source {
		t.Errorf("Expected metadata source to be %s, got %s", source, metadataSource)
	}
}

// TestBootstrapFlowSpecGeneration tests spec content generation for bootstrap.
func TestBootstrapFlowSpecGeneration(t *testing.T) {
	// Create a temporary spec file for testing
	tmpFile, err := os.CreateTemp("", "test-spec-*.md")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	testSpec := "# Test Bootstrap Spec\nThis is a test specification."
	if _, writeErr := tmpFile.WriteString(testSpec); writeErr != nil {
		t.Fatalf("Failed to write test spec: %v", writeErr)
	}
	tmpFile.Close()

	// Test with spec file
	flow := NewBootstrapFlow("", tmpFile.Name())
	ctx := context.Background()
	content, err := flow.loadSpecContent(ctx)

	if err != nil {
		t.Fatalf("loadSpecContent failed: %v", err)
	}

	if len(content) == 0 {
		t.Error("Expected non-empty spec content")
	}

	if string(content) != testSpec {
		t.Errorf("Expected spec content to match, got %s", string(content))
	}
}

// TestFlowRunnerInterface tests that both flows implement FlowRunner.
func TestFlowRunnerInterface(t *testing.T) {
	// Verify that both flow types implement the FlowRunner interface
	var bootstrapFlow FlowRunner = NewBootstrapFlow("", "")
	var mainFlow FlowRunner = NewMainFlow("", false)

	_ = bootstrapFlow // Verify it implements the interface
	_ = mainFlow      // Verify it implements the interface

	// Type assertions to ensure they're the correct concrete types
	if _, ok := bootstrapFlow.(*BootstrapFlow); !ok {
		t.Error("Expected concrete type BootstrapFlow")
	}

	if _, ok := mainFlow.(*OrchestratorFlow); !ok {
		t.Error("Expected concrete type OrchestratorFlow")
	}
}
