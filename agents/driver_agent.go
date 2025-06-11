package agents

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

// DriverBasedAgent wraps the Phase 3 state machine driver to implement the Agent interface
type DriverBasedAgent struct {
	id      string
	name    string
	workDir string
	logger  *logx.Logger
	driver  *agent.Driver
}

// NewDriverBasedAgent creates a new agent using the Phase 3 state machine driver
func NewDriverBasedAgent(id, name, workDir string, stateStore *state.Store, modelConfig *config.ModelCfg) *DriverBasedAgent {
	logger := logx.NewLogger(id)
	driver := agent.NewDriverWithModel(id, stateStore, modelConfig)
	
	return &DriverBasedAgent{
		id:      id,
		name:    name,
		workDir: workDir,
		logger:  logger,
		driver:  driver,
	}
}

// GetID returns the agent's identifier
func (a *DriverBasedAgent) GetID() string {
	return a.id
}

// ProcessMessage processes incoming messages using the state machine driver
func (a *DriverBasedAgent) ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	a.logger.Info("Processing message %s from %s", msg.ID, msg.FromAgent)

	switch msg.Type {
	case proto.MsgTypeTASK:
		return a.handleTaskMessage(ctx, msg)
	case proto.MsgTypeQUESTION:
		return a.handleQuestionMessage(ctx, msg)
	case proto.MsgTypeSHUTDOWN:
		return a.handleShutdownMessage(ctx, msg)
	default:
		return nil, fmt.Errorf("unsupported message type: %s", msg.Type)
	}
}

// Shutdown performs cleanup when the agent is stopping
func (a *DriverBasedAgent) Shutdown(ctx context.Context) error {
	a.logger.Info("Driver-based coding agent shutting down")
	// The state driver automatically persists state, so no additional cleanup needed
	return nil
}

func (a *DriverBasedAgent) handleTaskMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Initialize driver if needed
	if err := a.driver.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize driver: %w", err)
	}

	// Extract task content
	content, exists := msg.GetPayload("content")
	if !exists {
		return nil, fmt.Errorf("missing content in task message")
	}

	contentStr, ok := content.(string)
	if !ok {
		return nil, fmt.Errorf("content must be a string")
	}

	a.logger.Info("Processing coding task with state machine: %s", contentStr)

	// Process the task using the state machine
	if err := a.driver.ProcessTask(ctx, contentStr); err != nil {
		// Return error response
		response := proto.NewAgentMsg(proto.MsgTypeERROR, a.id, msg.FromAgent)
		response.ParentMsgID = msg.ID
		response.SetPayload("error", err.Error())
		response.SetPayload("original_message_id", msg.ID)
		response.SetMetadata("error_type", "processing_error")
		return response, nil
	}

	// Create successful result response
	result := proto.NewAgentMsg(proto.MsgTypeRESULT, a.id, msg.FromAgent)
	result.ParentMsgID = msg.ID
	result.SetPayload("status", "completed")
	result.SetPayload("final_state", string(a.driver.GetCurrentState()))
	
	// Add state machine context info
	stateData := a.driver.GetStateData()
	for key, value := range stateData {
		result.SetPayload(key, value)
	}
	
	result.SetPayload("context_summary", a.driver.GetContextSummary())
	result.SetMetadata("processing_agent", "driver-based")
	result.SetMetadata("task_type", "state_machine")

	// Extract story ID if available for traceability
	if storyID, exists := msg.GetPayload("story_id"); exists {
		if storyIDStr, ok := storyID.(string); ok {
			result.SetMetadata("story_id", storyIDStr)
		}
	}

	a.logger.Info("Completed task %s in state %s", msg.ID, a.driver.GetCurrentState())
	return result, nil
}

func (a *DriverBasedAgent) handleQuestionMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	question, exists := msg.GetPayload("question")
	if !exists {
		return nil, fmt.Errorf("missing question in message")
	}

	questionStr, ok := question.(string)
	if !ok {
		return nil, fmt.Errorf("question must be a string")
	}

	a.logger.Info("Received question: %s", questionStr)

	// Forward question to architect for guidance
	response := proto.NewAgentMsg(proto.MsgTypeQUESTION, a.id, "architect")
	response.ParentMsgID = msg.ID
	response.SetPayload("question", questionStr)
	response.SetPayload("context", "State machine driver question")
	response.SetPayload("current_state", string(a.driver.GetCurrentState()))
	response.SetMetadata("original_sender", msg.FromAgent)

	return response, nil
}

func (a *DriverBasedAgent) handleShutdownMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	a.logger.Info("Received shutdown request")

	response := proto.NewAgentMsg(proto.MsgTypeRESULT, a.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("status", "shutdown_acknowledged")
	response.SetPayload("final_state", string(a.driver.GetCurrentState()))
	response.SetMetadata("agent_type", "driver_based_coding_agent")

	return response, nil
}