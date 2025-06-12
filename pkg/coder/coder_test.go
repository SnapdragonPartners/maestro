package coder

import (
	"context"
	"testing"

	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

func TestNewCoder(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 32000,
		MaxReplyTokens:   4096,
		CompactionBuffer: 1000,
	}

	coder := NewCoder("test-coder", "test-coder", "./test-work", stateStore, modelConfig)

	if coder == nil {
		t.Error("Expected non-nil coder")
	}

	if coder.GetID() != "test-coder" {
		t.Errorf("Expected coder ID 'test-coder', got '%s'", coder.GetID())
	}
}

func TestCoder_ProcessTaskMessage(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 32000,
		MaxReplyTokens:   4096,
		CompactionBuffer: 1000,
	}

	coder := NewCoder("test-coder", "test-coder", tempDir, stateStore, modelConfig)
	ctx := context.Background()

	// Create a task message
	taskMsg := proto.NewAgentMsg(proto.MsgTypeTASK, "test-sender", "test-coder")
	taskMsg.SetPayload("content", "Create a simple hello world program")
	taskMsg.SetPayload("story_id", "test_001")

	// Process the message
	response, err := coder.ProcessMessage(ctx, taskMsg)
	if err != nil {
		t.Errorf("Expected no error processing task, got %v", err)
	}

	if response == nil {
		t.Error("Expected non-nil response")
	}

	if response.Type != proto.MsgTypeRESULT {
		t.Errorf("Expected response type RESULT, got %s", response.Type)
	}

	// Check response payload
	status, exists := response.GetPayload("status")
	if !exists {
		t.Error("Expected 'status' in response payload")
	}

	if statusStr, ok := status.(string); !ok || statusStr != "completed" {
		t.Errorf("Expected status 'completed', got %v", status)
	}

	// Check final state
	finalState, exists := response.GetPayload("final_state")
	if !exists {
		t.Error("Expected 'final_state' in response payload")
	}

	if finalStateStr, ok := finalState.(string); !ok || finalStateStr != "DONE" {
		t.Errorf("Expected final_state 'DONE', got %v", finalState)
	}

	// Check metadata
	processingAgent, exists := response.GetMetadata("processing_agent")
	if !exists || processingAgent != "coder" {
		t.Errorf("Expected processing_agent 'coder', got %s", processingAgent)
	}
}

func TestCoder_ProcessQuestionMessage(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 32000,
		MaxReplyTokens:   4096,
		CompactionBuffer: 1000,
	}

	coder := NewCoder("test-coder", "test-coder", tempDir, stateStore, modelConfig)
	ctx := context.Background()

	// Create a question message
	questionMsg := proto.NewAgentMsg(proto.MsgTypeQUESTION, "test-sender", "test-coder")
	questionMsg.SetPayload("question", "What pattern should I use for error handling?")

	// Process the message
	response, err := coder.ProcessMessage(ctx, questionMsg)
	if err != nil {
		t.Errorf("Expected no error processing question, got %v", err)
	}

	if response == nil {
		t.Error("Expected non-nil response")
	}

	if response.Type != proto.MsgTypeQUESTION {
		t.Errorf("Expected response type QUESTION, got %s", response.Type)
	}

	if response.ToAgent != "architect" {
		t.Errorf("Expected question to be forwarded to architect, got %s", response.ToAgent)
	}
}

func TestCoder_ProcessShutdownMessage(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 32000,
		MaxReplyTokens:   4096,
		CompactionBuffer: 1000,
	}

	coder := NewCoder("test-coder", "test-coder", tempDir, stateStore, modelConfig)
	ctx := context.Background()

	// Create a shutdown message
	shutdownMsg := proto.NewAgentMsg(proto.MsgTypeSHUTDOWN, "test-sender", "test-coder")

	// Process the message
	response, err := coder.ProcessMessage(ctx, shutdownMsg)
	if err != nil {
		t.Errorf("Expected no error processing shutdown, got %v", err)
	}

	if response == nil {
		t.Error("Expected non-nil response")
	}

	if response.Type != proto.MsgTypeRESULT {
		t.Errorf("Expected response type RESULT, got %s", response.Type)
	}

	// Check shutdown acknowledgment
	status, exists := response.GetPayload("status")
	if !exists {
		t.Error("Expected 'status' in response payload")
	}

	if statusStr, ok := status.(string); !ok || statusStr != "shutdown_acknowledged" {
		t.Errorf("Expected status 'shutdown_acknowledged', got %v", status)
	}
}
