package effect

import (
	"context"
	"errors"
	"testing"
	"time"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// mockDispatcher implements MessageDispatcher for testing.
type mockDispatcher struct {
	messages []*proto.AgentMsg
	errors   []error
	callIdx  int
}

func (m *mockDispatcher) DispatchMessage(msg *proto.AgentMsg) error {
	if m.callIdx < len(m.errors) && m.errors[m.callIdx] != nil {
		err := m.errors[m.callIdx]
		m.callIdx++
		return err
	}
	m.messages = append(m.messages, msg)
	m.callIdx++
	return nil
}

func TestBaseRuntime_ReceiveMessage_Success(t *testing.T) {
	// Setup
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	replyCh := make(chan *proto.AgentMsg, 1)
	runtime := NewBaseRuntime(dispatcher, logger, "test-agent", "test-role", replyCh)

	// Create expected message
	expectedMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect", "test-agent")

	// Send message to channel
	replyCh <- expectedMsg

	// Execute
	ctx := context.Background()
	receivedMsg, err := runtime.ReceiveMessage(ctx, proto.MsgTypeRESPONSE)

	// Assert
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if receivedMsg == nil {
		t.Fatal("Expected message, got nil")
	}
	if receivedMsg.ID != expectedMsg.ID {
		t.Errorf("Expected message ID %s, got %s", expectedMsg.ID, receivedMsg.ID)
	}
	if receivedMsg.Type != proto.MsgTypeRESPONSE {
		t.Errorf("Expected message type %s, got %s", proto.MsgTypeRESPONSE, receivedMsg.Type)
	}
	if receivedMsg.FromAgent != "architect" {
		t.Errorf("Expected from agent 'architect', got '%s'", receivedMsg.FromAgent)
	}
}

func TestBaseRuntime_ReceiveMessage_WrongType(t *testing.T) {
	// Setup
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	replyCh := make(chan *proto.AgentMsg, 1)
	runtime := NewBaseRuntime(dispatcher, logger, "test-agent", "test-role", replyCh)

	// Send message with wrong type
	wrongMsg := proto.NewAgentMsg(proto.MsgTypeERROR, "architect", "test-agent")
	replyCh <- wrongMsg

	// Execute - expect RESPONSE but get ERROR
	ctx := context.Background()
	receivedMsg, err := runtime.ReceiveMessage(ctx, proto.MsgTypeRESPONSE)

	// Assert
	if err == nil {
		t.Fatal("Expected error for wrong message type, got nil")
	}
	if receivedMsg != nil {
		t.Errorf("Expected nil message on error, got %+v", receivedMsg)
	}
	expectedErrMsg := "expected message type RESPONSE but received ERROR"
	if err.Error() != expectedErrMsg+" for agent test-agent" {
		t.Errorf("Expected error '%s', got '%s'", expectedErrMsg, err.Error())
	}
}

func TestBaseRuntime_ReceiveMessage_Timeout(t *testing.T) {
	// Setup
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	replyCh := make(chan *proto.AgentMsg, 1) // Empty channel - no message will arrive
	runtime := NewBaseRuntime(dispatcher, logger, "test-agent", "test-role", replyCh)

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Execute - should timeout
	receivedMsg, err := runtime.ReceiveMessage(ctx, proto.MsgTypeRESPONSE)

	// Assert
	if err == nil {
		t.Fatal("Expected timeout error, got nil")
	}
	if receivedMsg != nil {
		t.Errorf("Expected nil message on timeout, got %+v", receivedMsg)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected context.DeadlineExceeded in error chain, got: %v", err)
	}
}

func TestBaseRuntime_ReceiveMessage_NilChannel(t *testing.T) {
	// Setup - pass nil for replyCh
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	runtime := NewBaseRuntime(dispatcher, logger, "test-agent", "test-role", nil)

	// Execute
	ctx := context.Background()
	receivedMsg, err := runtime.ReceiveMessage(ctx, proto.MsgTypeRESPONSE)

	// Assert
	if err == nil {
		t.Fatal("Expected error for nil channel, got nil")
	}
	if receivedMsg != nil {
		t.Errorf("Expected nil message on error, got %+v", receivedMsg)
	}
	expectedErrMsg := "reply channel not configured for agent test-agent"
	if err.Error() != expectedErrMsg {
		t.Errorf("Expected error '%s', got '%s'", expectedErrMsg, err.Error())
	}
}

func TestBaseRuntime_ReceiveMessage_ClosedChannel(t *testing.T) {
	// Setup
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	replyCh := make(chan *proto.AgentMsg, 1)
	runtime := NewBaseRuntime(dispatcher, logger, "test-agent", "test-role", replyCh)

	// Close the channel before receiving
	close(replyCh)

	// Execute
	ctx := context.Background()
	receivedMsg, err := runtime.ReceiveMessage(ctx, proto.MsgTypeRESPONSE)

	// Assert
	if err == nil {
		t.Fatal("Expected error for closed channel, got nil")
	}
	if receivedMsg != nil {
		t.Errorf("Expected nil message on error, got %+v", receivedMsg)
	}
	expectedErrMsg := "reply channel closed unexpectedly for agent test-agent"
	if err.Error() != expectedErrMsg {
		t.Errorf("Expected error '%s', got '%s'", expectedErrMsg, err.Error())
	}
}

func TestBaseRuntime_ReceiveMessage_NilMessage(t *testing.T) {
	// Setup
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	replyCh := make(chan *proto.AgentMsg, 1)
	runtime := NewBaseRuntime(dispatcher, logger, "test-agent", "test-role", replyCh)

	// Send nil message to channel
	replyCh <- nil

	// Execute
	ctx := context.Background()
	receivedMsg, err := runtime.ReceiveMessage(ctx, proto.MsgTypeRESPONSE)

	// Assert
	if err == nil {
		t.Fatal("Expected error for nil message, got nil")
	}
	if receivedMsg != nil {
		t.Errorf("Expected nil message on error, got %+v", receivedMsg)
	}
	expectedErrMsg := "received nil message for agent test-agent"
	if err.Error() != expectedErrMsg {
		t.Errorf("Expected error '%s', got '%s'", expectedErrMsg, err.Error())
	}
}

func TestBaseRuntime_SendMessage_Success(t *testing.T) {
	// Setup
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	runtime := NewBaseRuntime(dispatcher, logger, "test-agent", "test-role", nil)

	// Create message
	msg := proto.NewAgentMsg(proto.MsgTypeSTORY, "test-agent", "coder-001")

	// Execute
	err := runtime.SendMessage(msg)

	// Assert
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if len(dispatcher.messages) != 1 {
		t.Fatalf("Expected 1 dispatched message, got %d", len(dispatcher.messages))
	}
	if dispatcher.messages[0].ID != msg.ID {
		t.Errorf("Expected message ID %s, got %s", msg.ID, dispatcher.messages[0].ID)
	}
}

func TestBaseRuntime_SendMessage_Error(t *testing.T) {
	// Setup - dispatcher that returns error
	expectedErr := errors.New("dispatch failed")
	dispatcher := &mockDispatcher{
		errors: []error{expectedErr},
	}
	logger := logx.NewLogger("test")
	runtime := NewBaseRuntime(dispatcher, logger, "test-agent", "test-role", nil)

	// Create message
	msg := proto.NewAgentMsg(proto.MsgTypeSTORY, "test-agent", "coder-001")

	// Execute
	err := runtime.SendMessage(msg)

	// Assert
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("Expected error to wrap dispatch error, got: %v", err)
	}
}

func TestBaseRuntime_GetAgentID(t *testing.T) {
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	runtime := NewBaseRuntime(dispatcher, logger, "test-agent-123", "test-role", nil)

	agentID := runtime.GetAgentID()
	if agentID != "test-agent-123" {
		t.Errorf("Expected agent ID 'test-agent-123', got '%s'", agentID)
	}
}

func TestBaseRuntime_GetAgentRole(t *testing.T) {
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	runtime := NewBaseRuntime(dispatcher, logger, "test-agent", "coder", nil)

	role := runtime.GetAgentRole()
	if role != "coder" {
		t.Errorf("Expected agent role 'coder', got '%s'", role)
	}
}

func TestBaseRuntime_LoggingMethods(_ *testing.T) {
	dispatcher := &mockDispatcher{}
	logger := logx.NewLogger("test")
	runtime := NewBaseRuntime(dispatcher, logger, "test-agent", "test-role", nil)

	// These should not panic
	runtime.Info("test info message")
	runtime.Error("test error message")
	runtime.Debug("test debug message")

	// If we get here without panic, test passes
}
