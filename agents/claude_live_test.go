package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/testkit"
)

func TestLiveClaudeAgent_Mock(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "workspace")

	// Create agent with mock mode (useLiveAPI = false)
	agent := NewLiveClaudeAgent("test-claude", "test-agent", workDir, "fake-key", false)

	// Test basic functionality
	if agent.GetID() != "test-claude" {
		t.Errorf("Expected ID 'test-claude', got '%s'", agent.GetID())
	}

	// Test workspace creation
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		t.Error("Workspace directory was not created")
	}

	// Test message processing with mock using testkit
	taskMsg := testkit.HealthEndpointTask("test-sender", "test-claude")

	ctx := context.Background()
	response, err := agent.ProcessMessage(ctx, taskMsg)
	if err != nil {
		t.Fatalf("Failed to process message: %v", err)
	}

	// Use testkit assertions
	testkit.AssertMessageType(t, response, proto.MsgTypeRESULT)
	testkit.AssertPayloadString(t, response, "status", "completed")
	testkit.AssertPayloadExists(t, response, "implementation")
	testkit.AssertTestResults(t, response, true)
	testkit.AssertHealthEndpointCode(t, response)
	testkit.AssertNoAPICallsMade(t, response)

	// Check workspace files
	files, err := filepath.Glob(filepath.Join(workDir, "*.go"))
	if err != nil {
		t.Fatalf("Failed to list workspace files: %v", err)
	}

	if len(files) == 0 {
		t.Error("Expected at least one Go file in workspace")
	}

	// Verify file content
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("Failed to read generated file: %v", err)
	}

	if len(content) == 0 {
		t.Error("Generated file is empty")
	}

	t.Logf("Generated file: %s (%d bytes)", files[0], len(content))
}

func TestLiveClaudeAgent_HealthEndpoint(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "workspace")

	agent := NewLiveClaudeAgent("test-claude", "test-agent", workDir, "fake-key", false)

	// Use testkit to create health endpoint task
	taskMsg := testkit.HealthEndpointTask("test-sender", "test-claude")

	ctx := context.Background()
	response, err := agent.ProcessMessage(ctx, taskMsg)
	if err != nil {
		t.Fatalf("Failed to process health endpoint task: %v", err)
	}

	// Use testkit assertions for comprehensive validation
	testkit.AssertMessageType(t, response, proto.MsgTypeRESULT)
	testkit.AssertHealthEndpointCode(t, response)
	testkit.AssertTestResults(t, response, true)
	testkit.AssertNoAPICallsMade(t, response)

	// Check workspace file
	healthFile := filepath.Join(workDir, "health.go")
	if _, err := os.Stat(healthFile); os.IsNotExist(err) {
		t.Error("Expected health.go file to be created in workspace")
	}
}

func TestLiveClaudeAgent_Shutdown(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "workspace")

	agent := NewLiveClaudeAgent("test-claude", "test-agent", workDir, "fake-key", false)

	// Use testkit to create shutdown message
	shutdownMsg := testkit.NewShutdownMessage("test-sender", "test-claude").
		WithMetadata("reason", "Test shutdown").
		Build()

	ctx := context.Background()
	response, err := agent.ProcessMessage(ctx, shutdownMsg)
	if err != nil {
		t.Fatalf("Failed to process shutdown message: %v", err)
	}

	// Use testkit assertions
	testkit.AssertMessageType(t, response, proto.MsgTypeRESULT)
	testkit.AssertPayloadString(t, response, "status", "shutdown_acknowledged")
}

func TestLiveClaudeAgent_ErrorHandling(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "workspace")

	agent := NewLiveClaudeAgent("test-claude", "test-agent", workDir, "fake-key", false)

	// Test with missing content using testkit
	taskMsg := testkit.NewTaskMessage("test-sender", "test-claude").
		Build() // Don't set content payload

	ctx := context.Background()
	response, err := agent.ProcessMessage(ctx, taskMsg)
	if err != nil {
		t.Fatalf("Expected error response, not processing error: %v", err)
	}

	// Use testkit assertions for error conditions
	testkit.AssertLintTestConditions(t, response, testkit.LintTestConditions{
		ShouldPass: false,
		ErrorText:  "Missing 'content'",
	})
}

func TestLiveClaudeAgent_LiveAPIMode(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "workspace")

	// Test that live API mode is properly configured
	agent := NewLiveClaudeAgent("test-claude", "test-agent", workDir, "fake-key", true)

	if !agent.useLiveAPI {
		t.Error("Expected useLiveAPI to be true")
	}

	if agent.client == nil {
		t.Error("Expected client to be initialized in live API mode")
	}

	// Test mock mode
	mockAgent := NewLiveClaudeAgent("test-claude", "test-agent", workDir, "", false)

	if mockAgent.useLiveAPI {
		t.Error("Expected useLiveAPI to be false in mock mode")
	}

	// Test live mode without API key
	noKeyAgent := NewLiveClaudeAgent("test-claude", "test-agent", workDir, "", true)

	if noKeyAgent.client != nil {
		t.Error("Expected client to be nil when no API key provided")
	}
}