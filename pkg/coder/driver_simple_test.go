//nolint:all // Legacy test file - needs migration to new APIs
package coder

import (
	"context"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

// Ultra-minimal test helper that only tests standalone methods.
func createBasicCoder(t *testing.T) *Coder {
	tempDir := t.TempDir()

	err := config.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	logger := logx.NewLogger("test-coder")
	contextMgr := contextmgr.NewContextManager()
	renderer, err := templates.NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	agentConfig := agent.NewConfig("test-coder-001", "coder", agent.Context{
		Context: context.Background(),
	})

	return &Coder{
		agentConfig:     agentConfig,
		agentID:         "test-coder-001",
		contextManager:  contextMgr,
		renderer:        renderer,
		logger:          logger,
		workDir:         tempDir,
		originalWorkDir: tempDir,
		codingBudget:    3,
	}
}

func TestCoderGetID(t *testing.T) {
	coder := createBasicCoder(t)

	if coder.GetID() != "test-coder-001" {
		t.Errorf("Expected ID test-coder-001, got %s", coder.GetID())
	}
}

func TestCoderGetAgentType(t *testing.T) {
	coder := createBasicCoder(t)

	if coder.GetAgentType() != agent.TypeCoder {
		t.Errorf("Expected agent type %v, got %v", agent.TypeCoder, coder.GetAgentType())
	}
}

func TestCoderGetContextSummary(t *testing.T) {
	coder := createBasicCoder(t)

	summary := coder.GetContextSummary()
	if len(summary) == 0 {
		t.Error("Expected non-empty context summary")
	}
}

func TestCoderChannelOperations(t *testing.T) {
	coder := createBasicCoder(t)

	storyCh := make(chan *proto.AgentMsg, 1)
	replyCh := make(chan *proto.AgentMsg, 1)

	// Test SetChannels
	coder.SetChannels(storyCh, nil, replyCh)

	// Verify channels were set
	if coder.storyCh != storyCh {
		t.Error("Expected story channel to be set")
	}
	if coder.replyCh != replyCh {
		t.Error("Expected reply channel to be set")
	}
}

func TestCoderSetDispatcher(t *testing.T) {
	coder := createBasicCoder(t)

	// Test SetDispatcher (should not panic)
	coder.SetDispatcher(nil)
}

func TestCoderDockerOperations(t *testing.T) {
	coder := createBasicCoder(t)

	// Test SetDockerImage (should not panic)
	coder.SetDockerImage("ubuntu:latest")

	// Test GetContainerName
	containerName := coder.GetContainerName()
	_ = containerName // Just ensure it doesn't panic
}

func TestCoderGetPendingApprovalRequest(t *testing.T) {
	coder := createBasicCoder(t)

	// Test GetPendingApprovalRequest
	hasPending, requestID, content, taskID, approvalType := coder.GetPendingApprovalRequest()
	if hasPending {
		t.Error("Expected no pending approval request initially")
	}
	_ = requestID
	_ = content
	_ = taskID
	_ = approvalType
}

func TestCoderClearPendingApprovalRequest(t *testing.T) {
	coder := createBasicCoder(t)

	// Test ClearPendingApprovalRequest (should not panic)
	coder.ClearPendingApprovalRequest()
}

func TestCoderToolProviders(t *testing.T) {
	coder := createBasicCoder(t)

	// Test with nil providers (default state)
	planningTools := coder.getPlanningToolsForLLM()
	if planningTools != nil {
		t.Error("Expected nil tools when planning provider is nil")
	}

	codingTools := coder.getCodingToolsForLLM()
	if codingTools != nil {
		t.Error("Expected nil tools when coding provider is nil")
	}
}

func TestRuntimeOperations(t *testing.T) {
	coder := createBasicCoder(t)
	runtime := NewRuntime(coder)

	// Test GetAgentID
	agentID := runtime.GetAgentID()
	if agentID != "test-coder-001" {
		t.Errorf("Expected agent ID test-coder-001, got %s", agentID)
	}

	// Test GetAgentRole
	role := runtime.GetAgentRole()
	if role != "coder" {
		t.Errorf("Expected agent role 'coder', got %s", role)
	}

	// Note: SendMessage and ReceiveMessage tests disabled as they require
	// proper dispatcher and channel setup which is beyond this basic test
}

// Simple effect implementation - no mocking, just basic struct.
type basicTestEffect struct {
	result any
	err    error
}

func (e *basicTestEffect) Execute(_ context.Context, _ effect.Runtime) (any, error) {
	return e.result, e.err
}

func (e *basicTestEffect) Type() string {
	return "basic_test"
}

func TestCoderExecuteEffect(t *testing.T) {
	coder := createBasicCoder(t)
	ctx := context.Background()

	// Test successful effect
	effect := &basicTestEffect{result: "success"}
	result, err := coder.ExecuteEffect(ctx, effect)
	if err != nil {
		t.Errorf("Expected no error from ExecuteEffect, got: %v", err)
	}
	if result != "success" {
		t.Errorf("Expected result 'success', got %v", result)
	}

	// Test effect with error
	effect = &basicTestEffect{err: context.DeadlineExceeded}
	result, err = coder.ExecuteEffect(ctx, effect)
	if err == nil {
		t.Error("Expected error from ExecuteEffect")
	}
	if result != nil {
		t.Error("Expected nil result when effect execution fails")
	}
}

// Test state constants - these are simple and don't require state machine.
func TestCoderStateConstants(t *testing.T) {
	states := []proto.State{
		StateSetup,
		StatePlanning,
		StateCoding,
		StateTesting,
		StatePlanReview,
		StateCodeReview,
		StateBudgetReview,
		StateAwaitMerge,
	}

	// Verify states are non-empty
	for _, state := range states {
		if len(state) == 0 {
			t.Error("Expected non-empty state constant")
		}
	}

	// Verify states are unique
	stateSet := make(map[proto.State]bool)
	for _, state := range states {
		if stateSet[state] {
			t.Errorf("Duplicate state found: %s", state)
		}
		stateSet[state] = true
	}
}

// Test helper functions that are just simple getters.
func TestCoderHelperFunctions(t *testing.T) {
	coder := createBasicCoder(t)

	// Test exploration history functions - these return hardcoded values
	history := coder.getExplorationHistory()
	if history == nil {
		t.Error("Expected non-nil exploration history")
	}

	files := coder.getFilesExamined()
	if files == nil {
		t.Error("Expected non-nil files examined")
	}

	findings := coder.getCurrentFindings()
	if findings == nil {
		t.Error("Expected non-nil current findings")
	}
}

// Test auto action constants.
func TestAutoActionConstants(t *testing.T) {
	actions := []AutoAction{
		AutoContinue,
		AutoPivot,
		AutoEscalate,
		AutoAbandon,
	}

	// Verify actions are non-empty
	for _, action := range actions {
		if len(action) == 0 {
			t.Error("Expected non-empty auto action constant")
		}
	}
}

// Test state data key constants.
func TestStateDataKeyConstants(t *testing.T) {
	keys := []string{
		KeyOrigin,
		KeyErrorMessage,
		KeyStoryMessageID,
		KeyStoryID,
		KeyQuestionSubmitted,
		KeyPlanSubmitted,
	}

	// Verify keys are non-empty
	for _, key := range keys {
		if len(key) == 0 {
			t.Error("Expected non-empty state data key constant")
		}
	}
}
