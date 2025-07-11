package coder

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

// Coder represents a unified coding agent implementation that uses the core state machine
type Coder struct {
	id      string
	name    string
	workDir string
	logger  *logx.Logger
	driver  *CoderDriver
}

// NewCoder creates a new coder agent using the core state machine (mock mode)
func NewCoder(id, name, workDir string, stateStore *state.Store, modelConfig *config.ModelCfg) (*Coder, error) {
	logger := logx.NewLogger(id)
	driver, err := NewCoderDriver(id, stateStore, modelConfig, nil, workDir, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create coder driver: %w", err)
	}

	coder := &Coder{
		id:      id,
		name:    name,
		workDir: workDir,
		logger:  logger,
		driver:  driver,
	}


	return coder, nil
}

// NewCoderWithLLM creates a new coder agent with LLM integration
func NewCoderWithLLM(id, name, workDir string, stateStore *state.Store, modelConfig *config.ModelCfg, llmClient agent.LLMClient) (*Coder, error) {
	logger := logx.NewLogger(id)
	driver, err := NewCoderDriver(id, stateStore, modelConfig, llmClient, workDir, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create coder driver: %w", err)
	}

	coder := &Coder{
		id:      id,
		name:    name,
		workDir: workDir,
		logger:  logger,
		driver:  driver,
	}


	return coder, nil
}

// NewCoderWithClaude creates a new coder agent with Claude LLM integration
func NewCoderWithClaude(id, name, workDir string, stateStore *state.Store, modelConfig *config.ModelCfg, apiKey string) (*Coder, error) {
	logger := logx.NewLogger(id)

	// Create Claude LLM client
	llmClient := agent.NewClaudeClient(apiKey)

	// Create driver with LLM integration
	driver, err := NewCoderDriver(id, stateStore, modelConfig, llmClient, workDir, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create coder driver: %w", err)
	}

	coder := &Coder{
		id:      id,
		name:    name,
		workDir: workDir,
		logger:  logger,
		driver:  driver,
	}


	return coder, nil
}

// GetID returns the coder's identifier
func (c *Coder) GetID() string {
	return c.id
}

// ProcessMessage processes incoming messages using the core state machine
func (c *Coder) ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	c.logger.Info("🧑‍💻 Coder %s processing message %s from %s (type: %s)", c.id, msg.ID, msg.FromAgent, msg.Type)

	switch msg.Type {
	case proto.MsgTypeTASK:
		return c.handleTaskMessage(ctx, msg)
	case proto.MsgTypeQUESTION:
		return c.handleQuestionMessage(ctx, msg)
	case proto.MsgTypeANSWER:
		return c.handleAnswerMessage(ctx, msg)
	case proto.MsgTypeREQUEST:
		return c.handleRequestMessage(ctx, msg)
	case proto.MsgTypeRESULT:
		return c.handleResultMessage(ctx, msg)
	case proto.MsgTypeSHUTDOWN:
		return c.handleShutdownMessage(ctx, msg)
	default:
		return nil, fmt.Errorf("unsupported message type: %s", msg.Type)
	}
}

// Shutdown performs cleanup when the coder is stopping
func (c *Coder) Shutdown(ctx context.Context) error {
	c.logger.Info("Coder agent shutting down")
	// The state driver automatically persists state, so no additional cleanup needed
	return nil
}

// GetDriver returns the coder driver for direct access (used by agentctl)
func (c *Coder) GetDriver() *CoderDriver {
	return c.driver
}

func (c *Coder) handleTaskMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Initialize driver if needed
	if err := c.driver.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize driver: %w", err)
	}

	// Extract task content
	content, exists := msg.GetPayload(proto.KeyContent)
	if !exists {
		return nil, fmt.Errorf("missing content in task message")
	}

	contentStr, ok := content.(string)
	if !ok {
		return nil, fmt.Errorf("content must be a string")
	}

	c.logger.Info("Processing coding task with state machine: %s", contentStr)

	// Process the task using the core state machine
	if err := c.driver.ProcessTask(ctx, contentStr); err != nil {
		// Return error response
		response := proto.NewAgentMsg(proto.MsgTypeERROR, c.id, msg.FromAgent)
		response.ParentMsgID = msg.ID
		response.SetPayload("error", err.Error())
		response.SetPayload("original_message_id", msg.ID)
		response.SetMetadata("error_type", "processing_error")
		return response, nil
	}

	// Check if there's a pending question for the architect
	if hasPending, questionID, questionContent, questionReason := c.driver.GetPendingQuestion(); hasPending {
		c.logger.Info("Sending QUESTION message to architect: %s (ID: %s)", questionReason, questionID)

		// Create QUESTION message for architect
		questionMsg := proto.NewAgentMsg(proto.MsgTypeQUESTION, c.id, "architect")
		questionMsg.ParentMsgID = msg.ID
		questionMsg.SetQuestionCorrelation(questionID) // Set correlation ID
		questionMsg.SetPayload(proto.KeyQuestion, questionContent)
		questionMsg.SetPayload(proto.KeyReason, questionReason)
		questionMsg.SetPayload(proto.KeyCurrentState, string(c.driver.GetCurrentState()))
		questionMsg.SetMetadata("original_sender", msg.FromAgent)
		questionMsg.SetMetadata("question_type", "state_machine_help")

		// Mark question as processed
		c.driver.ClearPendingQuestion()

		// Return the question message instead of a result
		return questionMsg, nil
	}

	// Check if there's a pending approval request for the architect
	if hasPending, approvalID, requestContent, requestReason, approvalType := c.driver.GetPendingApprovalRequest(); hasPending {
		c.logger.Info("Sending REQUEST message to architect for approval: %s (ID: %s)", requestReason, approvalID)

		// Create REQUEST message for architect approval
		approvalMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, c.id, "architect")
		approvalMsg.ParentMsgID = msg.ID
		approvalMsg.SetApprovalCorrelation(approvalID) // Set correlation ID
		approvalMsg.SetPayload(proto.KeyRequest, requestContent)
		approvalMsg.SetPayload(proto.KeyReason, requestReason)
		approvalMsg.SetPayload(proto.KeyCurrentState, string(c.driver.GetCurrentState()))
		approvalMsg.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
		approvalMsg.SetPayload(proto.KeyApprovalType, approvalType.String()) // Use explicit type from request
		approvalMsg.SetMetadata("original_sender", msg.FromAgent)
		approvalMsg.SetMetadata("message_type", "approval_request")

		// Mark approval request as processed
		c.driver.ClearPendingApprovalRequest()

		// Return the approval request message instead of a result
		return approvalMsg, nil
	}

	// Create successful result response
	result := proto.NewAgentMsg(proto.MsgTypeRESULT, c.id, msg.FromAgent)
	result.ParentMsgID = msg.ID
	result.SetPayload(proto.KeyStatus, "completed")
	result.SetPayload("final_state", string(c.driver.GetCurrentState()))

	// Add state machine context info
	stateData := c.driver.GetStateData()
	for key, value := range stateData {
		result.SetPayload(key, value)
	}

	result.SetPayload("context_summary", c.driver.GetContextSummary())
	result.SetMetadata("processing_agent", "coder")
	result.SetMetadata("task_type", "state_machine")

	// Extract story ID if available for traceability
	if storyID, exists := msg.GetPayload("story_id"); exists {
		if storyIDStr, ok := storyID.(string); ok {
			result.SetMetadata("story_id", storyIDStr)
		}
	}

	c.logger.Info("Completed task %s in state %s", msg.ID, c.driver.GetCurrentState())
	return result, nil
}

func (c *Coder) handleQuestionMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	question, exists := msg.GetPayload(proto.KeyQuestion)
	if !exists {
		return nil, fmt.Errorf("missing question in message")
	}

	questionStr, ok := question.(string)
	if !ok {
		return nil, fmt.Errorf("question must be a string")
	}

	c.logger.Info("Received question: %s", questionStr)

	// Forward question to architect for guidance
	response := proto.NewAgentMsg(proto.MsgTypeQUESTION, c.id, "architect")
	response.ParentMsgID = msg.ID
	response.SetPayload(proto.KeyQuestion, questionStr)
	response.SetPayload("context", "State machine driver question")
	response.SetPayload(proto.KeyCurrentState, string(c.driver.GetCurrentState()))
	response.SetMetadata("original_sender", msg.FromAgent)

	return response, nil
}

func (c *Coder) handleAnswerMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	answer, exists := msg.GetPayload(proto.KeyAnswer)
	if !exists {
		return nil, fmt.Errorf("missing answer in message")
	}

	answerStr, ok := answer.(string)
	if !ok {
		return nil, fmt.Errorf("answer must be a string")
	}

	c.logger.Info("Received answer from architect: %s", answerStr)

	// Initialize driver if needed
	if err := c.driver.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize driver: %w", err)
	}

	// Process the answer using the driver
	if err := c.driver.ProcessAnswer(answerStr); err != nil {
		return nil, fmt.Errorf("failed to process answer: %w", err)
	}

	// Continue processing the state machine with single step
	if _, err := c.driver.Step(ctx); err != nil {
		c.logger.Error("Failed to continue state machine processing: %v", err)
	}

	// Return acknowledgment
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, c.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload(proto.KeyStatus, "answer_received")
	response.SetPayload(proto.KeyAnswer, answerStr)
	return response, nil
}

func (c *Coder) handleRequestMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	request, exists := msg.GetPayload(proto.KeyRequest)
	if !exists {
		return nil, fmt.Errorf("missing request in message")
	}

	requestStr, ok := request.(string)
	if !ok {
		return nil, fmt.Errorf("request must be a string")
	}

	c.logger.Info("Received request: %s", requestStr)

	// Forward request to architect for approval
	response := proto.NewAgentMsg(proto.MsgTypeREQUEST, c.id, "architect")
	response.ParentMsgID = msg.ID
	response.SetPayload(proto.KeyRequest, requestStr)
	response.SetPayload("context", "Code approval request")
	response.SetPayload(proto.KeyCurrentState, string(c.driver.GetCurrentState()))
	response.SetMetadata("original_sender", msg.FromAgent)

	return response, nil
}

func (c *Coder) handleResultMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	status, exists := msg.GetPayload(proto.KeyStatus)
	if !exists {
		return nil, fmt.Errorf("missing status in result message")
	}

	statusStr, ok := status.(string)
	if !ok {
		return nil, fmt.Errorf("status must be a string")
	}

	c.logger.Info("Received approval result with status: %s", statusStr)

	// Debug logging for message handling
	logx.DebugToFile(ctx, "coder", "handle_result_debug.log", "handleResultMessage called - status=%s", statusStr)

	// Initialize driver if needed
	if err := c.driver.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize driver: %w", err)
	}

	// Determine if this is an approval result or answer
	// Try payload first, then metadata for request_type
	requestTypeStr := c.getStringFromPayloadOrMetadata(msg, proto.KeyRequestType)
	if requestTypeStr != "" {
		c.logger.Info("Found request_type: %v", requestTypeStr)

		// Parse and validate request type
		requestType, err := proto.ParseRequestType(requestTypeStr)
		if err != nil {
			c.logger.Error("Invalid request type '%s': %v", requestTypeStr, err)
			return nil, fmt.Errorf("invalid request type '%s': %w", requestTypeStr, err)
		}

		if requestType == proto.RequestApproval {
			// Handle approval result - try payload first, then metadata for approval_type
			approvalTypeRaw := c.getStringFromPayloadOrMetadata(msg, proto.KeyApprovalType)
			if approvalTypeRaw == "" {
				return nil, fmt.Errorf("missing approval_type in approval result message")
			}

			// Normalize and validate approval type
			approvalType, err := proto.NormaliseApprovalType(approvalTypeRaw)
			if err != nil {
				c.logger.Error("Invalid approval type '%s': %v", approvalTypeRaw, err)
				return nil, fmt.Errorf("invalid approval type '%s': %w", approvalTypeRaw, err)
			}

			c.logger.Info("Processing approval result: status=%s, type=%s", statusStr, approvalType)

			if err := c.driver.ProcessApprovalResult(statusStr, approvalType.String()); err != nil {
				return nil, fmt.Errorf("failed to process approval result: %w", err)
			}
			c.logger.Info("Successfully processed approval result")
		}
	} else if answer, exists := msg.GetPayload(proto.KeyAnswer); exists {
		// Handle answer to question
		answerStr, _ := answer.(string)
		if err := c.driver.ProcessAnswer(answerStr); err != nil {
			return nil, fmt.Errorf("failed to process answer: %w", err)
		}
	}

	// Continue processing the state machine with single step
	if _, err := c.driver.Step(ctx); err != nil {
		c.logger.Error("Failed to continue state machine processing: %v", err)
	}

	// Return acknowledgment
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, c.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload(proto.KeyStatus, "result_processed")
	response.SetPayload("original_status", statusStr)
	return response, nil
}

func (c *Coder) handleShutdownMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	c.logger.Info("Received shutdown request")

	response := proto.NewAgentMsg(proto.MsgTypeRESULT, c.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload(proto.KeyStatus, "shutdown_acknowledged")
	response.SetPayload("final_state", string(c.driver.GetCurrentState()))
	response.SetMetadata("agent_type", "coder")

	return response, nil
}

// getStringFromPayloadOrMetadata tries to get a string value from payload first, then metadata
func (c *Coder) getStringFromPayloadOrMetadata(msg *proto.AgentMsg, key string) string {
	// Try payload first
	if value, exists := msg.GetPayload(key); exists {
		if strValue, ok := value.(string); ok {
			return strValue
		}
	}

	// Try metadata
	if value, exists := msg.GetMetadata(key); exists {
		return value
	}

	return ""
}
