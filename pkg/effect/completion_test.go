package effect

import (
	"context"
	"testing"

	"orchestrator/pkg/proto"
)

// MockRuntime provides a mock implementation of the Runtime interface for testing.
type MockRuntime struct {
	agentID   string
	agentRole string
	messages  []string
}

func NewMockRuntime(agentID, agentRole string) *MockRuntime {
	return &MockRuntime{
		agentID:   agentID,
		agentRole: agentRole,
		messages:  make([]string, 0),
	}
}

func (m *MockRuntime) SendMessage(_ *proto.AgentMsg) error {
	m.messages = append(m.messages, "SendMessage called")
	return nil
}

func (m *MockRuntime) ReceiveMessage(_ context.Context, _ proto.MsgType) (*proto.AgentMsg, error) {
	m.messages = append(m.messages, "ReceiveMessage called")
	return &proto.AgentMsg{}, nil
}

func (m *MockRuntime) Info(msg string, _ ...any) {
	m.messages = append(m.messages, "Info: "+msg)
}

func (m *MockRuntime) Error(msg string, _ ...any) {
	m.messages = append(m.messages, "Error: "+msg)
}

func (m *MockRuntime) Debug(msg string, _ ...any) {
	m.messages = append(m.messages, "Debug: "+msg)
}

func (m *MockRuntime) GetAgentID() string {
	return m.agentID
}

func (m *MockRuntime) GetAgentRole() string {
	return m.agentRole
}

func TestCompletionEffectExecution(t *testing.T) {
	// Create a completion effect
	completionEff := NewCompletionEffect(
		"Test task completed successfully",
		proto.State("TESTING"),
	)

	// Create mock runtime
	runtime := NewMockRuntime("test-coder", "coder")

	// Execute the effect
	ctx := context.Background()
	result, err := completionEff.Execute(ctx, runtime)

	// Verify no error occurred
	if err != nil {
		t.Errorf("CompletionEffect execution should not return error, got: %v", err)
	}

	// Verify the result type
	completionResult, ok := result.(*CompletionResult)
	if !ok {
		t.Errorf("Expected CompletionResult, got: %T", result)
	}

	// Verify the result properties
	if completionResult.TargetState != proto.State("TESTING") {
		t.Errorf("Expected target state 'TESTING', got: %s", completionResult.TargetState)
	}

	if completionResult.Message != "Test task completed successfully" {
		t.Errorf("Expected message 'Test task completed successfully', got: %s", completionResult.Message)
	}

	// Verify that Info was called (check for any Info message)
	infoWasCalled := false
	for _, msg := range runtime.messages {
		if len(msg) > 5 && msg[:5] == "Info:" {
			infoWasCalled = true
			t.Logf("Info message logged: %s", msg)
			break
		}
	}
	if !infoWasCalled {
		t.Errorf("Expected Info logging to be called during effect execution, messages: %v", runtime.messages)
	}

	t.Logf("✅ CompletionEffect execution works correctly")
}

func TestCompletionEffectWithMetadata(t *testing.T) {
	// Create metadata
	metadata := map[string]any{
		"files_created": []string{"main.go", "test.go"},
		"duration":      "5m30s",
		"complexity":    "medium",
	}

	// Create a completion effect with metadata
	completionEff := NewCompletionEffectWithMetadata(
		"Implementation completed with metadata",
		proto.State("TESTING"),
		metadata,
	)

	// Create mock runtime
	runtime := NewMockRuntime("test-coder", "coder")

	// Execute the effect
	ctx := context.Background()
	result, err := completionEff.Execute(ctx, runtime)

	// Verify no error occurred
	if err != nil {
		t.Errorf("CompletionEffect execution should not return error, got: %v", err)
	}

	// Verify the result type and metadata
	completionResult, ok := result.(*CompletionResult)
	if !ok {
		t.Errorf("Expected CompletionResult, got: %T", result)
	}

	if completionResult.Metadata == nil {
		t.Error("Expected metadata to be present in result")
	}

	if len(completionResult.Metadata) != len(metadata) {
		t.Errorf("Expected metadata length %d, got: %d", len(metadata), len(completionResult.Metadata))
	}

	// Verify specific metadata values
	if filesCreated, exists := completionResult.Metadata["files_created"]; exists {
		if files, ok := filesCreated.([]string); ok {
			if len(files) != 2 || files[0] != "main.go" || files[1] != "test.go" {
				t.Errorf("Expected files_created ['main.go', 'test.go'], got: %v", files)
			}
		} else {
			t.Errorf("Expected files_created to be []string, got: %T", filesCreated)
		}
	} else {
		t.Error("Expected files_created metadata to be present")
	}

	t.Logf("✅ CompletionEffect with metadata works correctly")
}

func TestCompletionEffectType(t *testing.T) {
	// Test the effect type identifier
	completionEff := NewCompletionEffect("test", proto.State("TESTING"))

	if completionEff.Type() != "completion" {
		t.Errorf("Expected effect type 'completion', got: %s", completionEff.Type())
	}

	t.Logf("✅ CompletionEffect type identifier is correct")
}
