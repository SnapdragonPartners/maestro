package agents

import (
	"context"
	"testing"

	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

func TestNewDriverBasedAgent(t *testing.T) {
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
	
	agent := NewDriverBasedAgent("test-agent", "test-claude", "./test-work", stateStore, modelConfig)
	
	if agent == nil {
		t.Error("Expected non-nil agent")
	}
	
	if agent.GetID() != "test-agent" {
		t.Errorf("Expected agent ID 'test-agent', got '%s'", agent.GetID())
	}
}

func TestDriverBasedAgent_ProcessTaskMessage(t *testing.T) {
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
	
	agent := NewDriverBasedAgent("test-agent", "test-claude", tempDir, stateStore, modelConfig)
	ctx := context.Background()
	
	// Create a task message
	taskMsg := proto.NewAgentMsg(proto.MsgTypeTASK, "test-sender", "test-agent")
	taskMsg.SetPayload("content", "Create a simple hello world program")
	taskMsg.SetPayload("story_id", "test_001")
	
	// Process the message
	response, err := agent.ProcessMessage(ctx, taskMsg)
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
	if !exists || processingAgent != "driver-based" {
		t.Errorf("Expected processing_agent 'driver-based', got %s", processingAgent)
	}
}

func TestDriverBasedAgent_ProcessQuestionMessage(t *testing.T) {
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
	
	agent := NewDriverBasedAgent("test-agent", "test-claude", tempDir, stateStore, modelConfig)
	ctx := context.Background()
	
	// Create a question message
	questionMsg := proto.NewAgentMsg(proto.MsgTypeQUESTION, "test-sender", "test-agent")
	questionMsg.SetPayload("question", "What pattern should I use for error handling?")
	
	// Process the message
	response, err := agent.ProcessMessage(ctx, questionMsg)
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

func TestDriverBasedAgent_ProcessShutdownMessage(t *testing.T) {
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
	
	agent := NewDriverBasedAgent("test-agent", "test-claude", tempDir, stateStore, modelConfig)
	ctx := context.Background()
	
	// Create a shutdown message
	shutdownMsg := proto.NewAgentMsg(proto.MsgTypeSHUTDOWN, "test-sender", "test-agent")
	
	// Process the message
	response, err := agent.ProcessMessage(ctx, shutdownMsg)
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