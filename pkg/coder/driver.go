package coder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/state"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
)

// State represents the current state of the coder workflow (v2 FSM)
type State string

const (
	StateWaiting     State = "WAITING"
	StatePlanning    State = "PLANNING"
	StatePlanReview  State = "PLAN_REVIEW"
	StateCoding      State = "CODING"
	StateTesting     State = "TESTING"
	StateFixing      State = "FIXING"
	StateCodeReview  State = "CODE_REVIEW"
	StateDone        State = "DONE"
	StateError       State = "ERROR"
	StateQuestion    State = "QUESTION"
)

// Driver manages the state machine for a coder workflow
type Driver struct {
	agentID        string
	stateStore     *state.Store
	contextManager *contextmgr.ContextManager
	currentState   State
	stateData      map[string]interface{}
	llmClient      agent.LLMClient     // Optional LLM for live mode
	renderer       *templates.Renderer // Template renderer for prompts
	workDir        string              // Workspace directory for MCP tool calls
}

// NewDriver creates a new coder driver instance
func NewDriver(agentID string, stateStore *state.Store) *Driver {
	renderer, _ := templates.NewRenderer() // Ignore error for now, fallback to mock mode
	return &Driver{
		agentID:        agentID,
		stateStore:     stateStore,
		contextManager: contextmgr.NewContextManager(),
		currentState:   StateWaiting, // Default starting state for v2 FSM
		stateData:      make(map[string]interface{}),
		llmClient:      nil, // No LLM - mock mode
		renderer:       renderer,
	}
}

// NewDriverWithModel creates a new coder driver with model configuration
func NewDriverWithModel(agentID string, stateStore *state.Store, modelConfig *config.ModelCfg, workDir string) *Driver {
	renderer, _ := templates.NewRenderer() // Ignore error for now, fallback to mock mode
	return &Driver{
		agentID:        agentID,
		stateStore:     stateStore,
		contextManager: contextmgr.NewContextManagerWithModel(modelConfig),
		currentState:   StateWaiting, // Default starting state for v2 FSM
		stateData:      make(map[string]interface{}),
		llmClient:      nil, // No LLM - mock mode
		renderer:       renderer,
		workDir:        workDir,
	}
}

// NewDriverWithLLM creates a new coder driver with LLM integration for live mode
func NewDriverWithLLM(agentID string, stateStore *state.Store, modelConfig *config.ModelCfg, llmClient agent.LLMClient, workDir string) *Driver {
	renderer, _ := templates.NewRenderer() // Ignore error for now, fallback to mock mode
	return &Driver{
		agentID:        agentID,
		stateStore:     stateStore,
		contextManager: contextmgr.NewContextManagerWithModel(modelConfig),
		currentState:   StateWaiting, // Default starting state for v2 FSM
		stateData:      make(map[string]interface{}),
		llmClient:      llmClient, // Live LLM mode
		renderer:       renderer,
		workDir:        workDir,
	}
}

// Initialize sets up the driver and loads any existing state
func (d *Driver) Initialize(ctx context.Context) error {
	// Load existing state if available
	savedState, savedData, err := d.stateStore.LoadState(d.agentID)
	if err != nil {
		return fmt.Errorf("failed to load state for agent %s: %w", d.agentID, err)
	}

	// If we have saved state, restore it
	if savedState != "" {
		d.currentState = State(savedState)
		d.stateData = savedData
	}

	return nil
}

// ProcessTask runs the main state machine loop for processing a task
func (d *Driver) ProcessTask(ctx context.Context, taskContent string) error {
	// Reset state data for new task (but preserve agent configuration)
	d.resetStateForNewTask()

	// Add initial task to context
	d.contextManager.AddMessage("user", taskContent)

	// Store initial task data
	d.stateData["task_content"] = taskContent
	d.stateData["started_at"] = time.Now().UTC()

	// Run the state machine loop
	maxIterations := 100 // Prevent infinite loops
	iteration := 0

mainLoop:
	for iteration < maxIterations {
		iteration++

		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if we're already in a terminal state
		if d.currentState == StateDone || d.currentState == StateError {
			break mainLoop
		}

		// Process current state
		nextState, err := d.processCurrentState(ctx)
		if err != nil {
			// Transition to error state
			d.transitionTo(ctx, StateError, map[string]interface{}{
				"error":        err.Error(),
				"failed_state": string(d.currentState),
			})
			return err
		}

		// Transition to next state
		if nextState != d.currentState {
			d.transitionTo(ctx, nextState, nil)
		} else {
			// If state didn't change and it's not a terminal state, something is wrong
			return fmt.Errorf("state machine stuck in state %s", d.currentState)
		}

		// Compact context if needed
		if err := d.contextManager.CompactIfNeeded(); err != nil {
			// Log warning but don't fail
			fmt.Printf("Warning: context compaction failed: %v\n", err)
		}
	}

	if iteration >= maxIterations {
		return fmt.Errorf("state machine exceeded maximum iterations (%d)", maxIterations)
	}

	return nil
}

// processCurrentState handles the logic for the current state (v2 FSM)
func (d *Driver) processCurrentState(ctx context.Context) (State, error) {
	switch d.currentState {
	case StateWaiting:
		return d.handleWaitingState(ctx)
	case StatePlanning:
		return d.handlePlanningState(ctx)
	case StatePlanReview:
		return d.handlePlanReviewState(ctx)
	case StateCoding:
		return d.handleCodingState(ctx)
	case StateTesting:
		return d.handleTestingState(ctx)
	case StateFixing:
		return d.handleFixingState(ctx)
	case StateCodeReview:
		return d.handleCodeReviewState(ctx)
	case StateQuestion:
		return d.handleQuestionState(ctx)
	case StateDone:
		// DONE is a terminal state - should not continue processing
		return StateDone, nil
	case StateError:
		// ERROR is a terminal state - should not continue processing
		return StateError, nil
	default:
		return StateError, fmt.Errorf("unknown state: %s", d.currentState)
	}
}

// handleWaitingState processes the waiting phase (waiting for TASK)
func (d *Driver) handleWaitingState(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Waiting for task assignment")
	
	// In the v2 FSM, WAITING is the initial state where we wait for a TASK message
	// This state should not advance automatically - it waits for external task assignment
	// For now, if we have task content, transition to PLANNING
	if taskContent, exists := d.stateData["task_content"]; exists && taskContent != "" {
		return StatePlanning, nil
	}
	
	// Stay in WAITING state until task is received
	return StateWaiting, nil
}

// handlePlanningState processes the planning phase
func (d *Driver) handlePlanningState(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Planning phase: analyzing requirements")

	// Check if help is requested (need clarification)
	taskContent, _ := d.stateData["task_content"].(string)
	if detectHelpRequest(taskContent) {
		d.stateData["question_reason"] = "Help requested during planning phase"
		d.stateData["question_content"] = taskContent
		d.stateData["question_origin"] = "PLANNING" // v2 FSM: track origin state
		return StateQuestion, nil
	}

	if d.llmClient != nil {
		// Use LLM for planning
		return d.handlePlanningWithLLM(ctx)
	} else {
		// Fallback to mock mode
		return d.handlePlanningMock(ctx)
	}
}

// handlePlanningWithLLM uses the LLM to generate a plan
func (d *Driver) handlePlanningWithLLM(ctx context.Context) (State, error) {
	taskContent, _ := d.stateData["task_content"].(string)
	contextStr := d.formatContextAsString()

	// Render the planning template
	templateData := &templates.TemplateData{
		TaskContent: taskContent,
		Context:     contextStr,
	}

	prompt, err := d.renderer.Render(templates.PlanningTemplate, templateData)
	if err != nil {
		return StateError, fmt.Errorf("failed to render planning template: %w", err)
	}

	// Get LLM response
	response, err := d.llmClient.GenerateResponse(ctx, prompt)
	if err != nil {
		return StateError, fmt.Errorf("failed to get LLM response for planning: %w", err)
	}

	// Add LLM response to context
	d.contextManager.AddMessage("assistant", response)

	// Parse the response to extract plan
	plan, _ := d.parsePlanningResponse(response)
	d.stateData["plan"] = plan
	d.stateData["planning_completed_at"] = time.Now().UTC()

	// v2 FSM: After planning, always go to PLAN_REVIEW for architect approval
	return StatePlanReview, nil
}

// handlePlanningMock provides the original mock planning behavior
func (d *Driver) handlePlanningMock(ctx context.Context) (State, error) {
	// Simulate some planning work
	d.stateData["plan"] = "Analyzed requirements, ready to proceed with implementation"
	d.stateData["planning_completed_at"] = time.Now().UTC()

	// v2 FSM: After planning, always go to PLAN_REVIEW for architect approval
	return StatePlanReview, nil
}

// handlePlanReviewState processes plan review by architect
func (d *Driver) handlePlanReviewState(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Plan review phase: requesting architect approval")

	// Check if we need to send a REQUEST message to architect
	if !d.hasPlanReviewRequest() {
		d.stateData["pending_plan_review_request"] = true
		d.stateData["plan_review_request_content"] = d.generatePlanReviewRequestContent()
		d.stateData["plan_review_request_reason"] = "Plan completed, requesting architect review"
	}

	// For now, simulate architect response based on task content (like old approval logic)
	taskContent, _ := d.stateData["task_content"].(string)
	var approved bool
	var reviewReason string

	if strings.Contains(strings.ToLower(taskContent), "approve") ||
		strings.Contains(strings.ToLower(taskContent), "looks good") {
		approved = true
		reviewReason = "Architect approved plan"
	} else if strings.Contains(strings.ToLower(taskContent), "change plan") ||
		strings.Contains(strings.ToLower(taskContent), "revise plan") {
		approved = false
		reviewReason = "Architect requested plan changes"
	} else {
		// Default approval for normal tasks
		approved = true
		reviewReason = "Architect approved plan (default)"
	}

	d.stateData["plan_review_status"] = approved
	d.stateData["plan_review_reason"] = reviewReason
	d.stateData["plan_review_completed_at"] = time.Now().UTC()

	// Clear the pending request flag
	delete(d.stateData, "pending_plan_review_request")

	if approved {
		return StateCoding, nil
	} else {
		return StatePlanning, nil // Go back to planning for revisions
	}
}

// handleCodingState processes the coding phase
func (d *Driver) handleCodingState(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Coding phase: implementing solution")

	// Check if help is requested during coding
	taskContent, _ := d.stateData["task_content"].(string)
	if detectHelpRequest(taskContent) && !d.hasAnsweredCodingQuestion() {
		d.stateData["question_reason"] = "Technical question during coding"
		d.stateData["question_content"] = "Need clarification on implementation approach"
		d.stateData["question_origin"] = "CODING" // v2 FSM: track origin state
		return StateQuestion, nil
	}

	if d.llmClient != nil {
		// Use LLM for code generation
		return d.handleCodingWithLLM(ctx)
	} else {
		// Fallback to mock mode
		return d.handleCodingMock(ctx)
	}
}

// handleCodingWithLLM uses the LLM to generate actual code
func (d *Driver) handleCodingWithLLM(ctx context.Context) (State, error) {
	taskContent, _ := d.stateData["task_content"].(string)
	plan, _ := d.stateData["plan"].(string)
	contextStr := d.formatContextAsString()

	// Get tool results if available
	previousToolResults := ""
	if toolRes, exists := d.stateData["tool_results"]; exists {
		if toolMap, ok := toolRes.(map[string]interface{}); ok {
			if stdout, ok := toolMap["stdout"].(string); ok {
				previousToolResults = stdout
			}
		}
	}

	// Render the coding template
	templateData := &templates.TemplateData{
		TaskContent: taskContent,
		Context:     contextStr,
		Plan:        plan,
		ToolResults: previousToolResults,
		WorkDir:     d.workDir,
	}

	prompt, err := d.renderer.Render(templates.CodingTemplate, templateData)
	if err != nil {
		return StateError, fmt.Errorf("failed to render coding template: %w", err)
	}

	// Get LLM response
	response, err := d.llmClient.GenerateResponse(ctx, prompt)
	if err != nil {
		return StateError, fmt.Errorf("failed to get LLM response for coding: %w", err)
	}

	// Add LLM response to context
	d.contextManager.AddMessage("assistant", response)

	// Parse and execute any MCP tool calls in the response
	toolCalls, err := tools.ParseToolCalls(response)
	if err != nil {
		return StateError, fmt.Errorf("failed to parse tool calls from coding response: %w", err)
	}

	// Execute the tool calls
	var toolResults []map[string]interface{}
	for _, call := range toolCalls {
		result, err := d.executeToolCall(ctx, call)
		if err != nil {
			return StateError, fmt.Errorf("failed to execute tool call %s: %w", call.Name, err)
		}
		toolResults = append(toolResults, result)
	}

	d.stateData["code_generated"] = true
	d.stateData["implementation"] = response
	d.stateData["tool_calls_executed"] = len(toolCalls)
	d.stateData["coding_completed_at"] = time.Now().UTC()

	// v2 FSM: After coding, always go to TESTING
	return StateTesting, nil
}

// handleCodingMock provides the original mock coding behavior
func (d *Driver) handleCodingMock(ctx context.Context) (State, error) {
	// Simulate code generation
	d.stateData["code_generated"] = true
	d.stateData["coding_completed_at"] = time.Now().UTC()

	// v2 FSM: After coding, always go to TESTING
	return StateTesting, nil
}

// handleTestingState processes the testing phase
func (d *Driver) handleTestingState(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Testing phase: running tests")

	// Check if we should simulate a test failure for Story 033 demonstration
	taskContent, _ := d.stateData["task_content"].(string)
	shouldFailTests := strings.Contains(strings.ToLower(taskContent), "test fail") ||
		strings.Contains(strings.ToLower(taskContent), "simulate fail")

	// Check if we've already tried fixing to avoid infinite loops
	_, alreadyFixed := d.stateData["fixes_applied"]

	var testsPassed bool
	if shouldFailTests && !alreadyFixed {
		// Simulate test failure on first attempt
		testsPassed = false
		d.stateData["test_failure_reason"] = "Simulated test failure for Story 033 demonstration"
	} else {
		// Tests pass (either naturally or after fixing)
		testsPassed = true
	}

	d.stateData["tests_passed"] = testsPassed
	d.stateData["testing_completed_at"] = time.Now().UTC()

	if !testsPassed {
		if alreadyFixed {
			// If we've already tried fixing and tests still fail, give up
			return StateError, fmt.Errorf("tests failed after fixing attempts")
		}
		// v2 FSM: On test failure, go to FIXING
		return StateFixing, nil
	}

	// v2 FSM: On test success, go to CODE_REVIEW for architect approval
	return StateCodeReview, nil
}

// handleCodeReviewState processes code review by architect
func (d *Driver) handleCodeReviewState(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Code review phase: requesting architect approval")

	// Check if we need to send a REQUEST message to architect
	if !d.hasCodeReviewRequest() {
		d.stateData["pending_code_review_request"] = true
		d.stateData["code_review_request_content"] = d.generateCodeReviewRequestContent()
		d.stateData["code_review_request_reason"] = "Code completed, requesting architect review"
	}

	// For now, simulate architect response based on task content
	taskContent, _ := d.stateData["task_content"].(string)
	var approved bool
	var reviewReason string

	if strings.Contains(strings.ToLower(taskContent), "approve") ||
		strings.Contains(strings.ToLower(taskContent), "looks good") {
		approved = true
		reviewReason = "Architect approved code"
	} else if strings.Contains(strings.ToLower(taskContent), "change") ||
		strings.Contains(strings.ToLower(taskContent), "fix") ||
		strings.Contains(strings.ToLower(taskContent), "modify") {
		// Check if we've already been through fixing to avoid infinite loops
		fixesApplied, _ := d.stateData["fixes_applied"].(bool)
		if fixesApplied {
			// After one round of fixes, approve to avoid infinite loop
			approved = true
			reviewReason = "Architect approved code after changes"
		} else {
			approved = false
			reviewReason = "Architect requested code changes"
		}
	} else {
		// Default approval for normal tasks
		approved = true
		reviewReason = "Architect approved code (default)"
	}

	d.stateData["code_review_status"] = approved
	d.stateData["code_review_reason"] = reviewReason
	d.stateData["code_review_completed_at"] = time.Now().UTC()

	// Clear the pending request flag
	delete(d.stateData, "pending_code_review_request")

	if approved {
		return StateDone, nil
	} else {
		// v2 FSM: On code rejection, go to FIXING
		return StateFixing, nil
	}
}


// handleFixingState processes the fixing phase
func (d *Driver) handleFixingState(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Fixing phase: addressing issues")

	// Simulate fixing work
	d.stateData["fixes_applied"] = true
	d.stateData["fixing_completed_at"] = time.Now().UTC()

	// v2 FSM: After fixing, return to CODING
	return StateCoding, nil
}

// handleQuestionState processes the question phase (v2 FSM with origin metadata)
func (d *Driver) handleQuestionState(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Question phase: awaiting clarification")

	// Mark that we need to send a question to the architect
	d.stateData["pending_question"] = true
	d.stateData["question_state"] = "QUESTION"
	d.stateData["question_completed_at"] = time.Now().UTC()

	// For demonstration, simulate getting an immediate answer
	// In a full implementation, this would wait for architect response
	d.stateData["question_answered"] = true
	d.stateData["architect_response"] = "Simulated architect guidance received"

	// v2 FSM: Use origin metadata to return to the correct state
	origin, _ := d.stateData["question_origin"].(string)
	switch origin {
	case "PLANNING":
		return StatePlanning, nil
	case "CODING":
		return StateCoding, nil
	case "FIXING":
		return StateFixing, nil
	default:
		// Fallback to PLANNING if origin is unknown
		return StatePlanning, nil
	}
}

// transitionTo moves the driver to a new state and persists it
func (d *Driver) transitionTo(ctx context.Context, newState State, additionalData map[string]interface{}) error {
	oldState := d.currentState
	d.currentState = newState

	// Add transition metadata
	d.stateData["previous_state"] = string(oldState)
	d.stateData["current_state"] = string(newState)
	d.stateData["transition_at"] = time.Now().UTC()

	// Merge additional data if provided
	if additionalData != nil {
		for k, v := range additionalData {
			d.stateData[k] = v
		}
	}

	// Persist state
	if err := d.stateStore.SaveState(d.agentID, string(newState), d.stateData); err != nil {
		return fmt.Errorf("failed to persist state transition from %s to %s: %w", oldState, newState, err)
	}

	return nil
}

// GetCurrentState returns the current state of the driver
func (d *Driver) GetCurrentState() State {
	return d.currentState
}

// GetStateData returns a copy of the current state data
func (d *Driver) GetStateData() map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range d.stateData {
		result[k] = v
	}
	return result
}

// GetContextSummary returns a summary of the current context
func (d *Driver) GetContextSummary() string {
	return d.contextManager.GetContextSummary()
}

// GetPendingQuestion returns question details if there's a pending question for the architect
func (d *Driver) GetPendingQuestion() (bool, string, string) {
	hasPending, _ := d.stateData["pending_question"].(bool)
	if !hasPending {
		return false, "", ""
	}

	questionContent, _ := d.stateData["question_content"].(string)
	questionReason, _ := d.stateData["question_reason"].(string)

	return true, questionContent, questionReason
}

// ClearPendingQuestion marks the pending question as processed
func (d *Driver) ClearPendingQuestion() {
	delete(d.stateData, "pending_question")
}

// GetPendingApprovalRequest returns approval request details if there's a pending request for the architect
func (d *Driver) GetPendingApprovalRequest() (bool, string, string) {
	hasPending, _ := d.stateData["pending_approval_request"].(bool)
	if !hasPending {
		return false, "", ""
	}

	requestContent, _ := d.stateData["approval_request_content"].(string)
	requestReason, _ := d.stateData["approval_request_reason"].(string)

	return true, requestContent, requestReason
}

// ClearPendingApprovalRequest marks the pending approval request as processed
func (d *Driver) ClearPendingApprovalRequest() {
	delete(d.stateData, "pending_approval_request")
}

// resetStateForNewTask clears state data that should not persist between tasks
func (d *Driver) resetStateForNewTask() {
	// Clear task-specific state but preserve agent configuration
	keysToKeep := map[string]bool{
		"agent_id":      true,
		"workspace_dir": true,
		"initialized":   true,
	}

	// Clear all state except keys to keep
	newStateData := make(map[string]interface{})
	for key, value := range d.stateData {
		if keysToKeep[key] {
			newStateData[key] = value
		}
	}
	d.stateData = newStateData

	// v2 FSM: Reset to initial state
	d.currentState = StateWaiting
}

// hasApprovalRequest checks if an approval request has already been sent
func (d *Driver) hasApprovalRequest() bool {
	_, exists := d.stateData["approval_request_sent"]
	return exists
}

// generateApprovalRequestContent creates content for the approval request
func (d *Driver) generateApprovalRequestContent() string {
	taskContent, _ := d.stateData["task_content"].(string)
	plan, _ := d.stateData["plan"].(string)

	content := fmt.Sprintf("Implementation completed. Please review:\n\nOriginal Task: %s\n\nPlan: %s\n\nCode generation: completed\nTests: passed\n\nReady for approval.",
		taskContent, plan)

	// Mark that we've sent the request
	d.stateData["approval_request_sent"] = true

	return content
}

// hasPlanReviewRequest checks if a plan review request has already been sent
func (d *Driver) hasPlanReviewRequest() bool {
	_, exists := d.stateData["plan_review_request_sent"]
	return exists
}

// generatePlanReviewRequestContent creates content for the plan review request
func (d *Driver) generatePlanReviewRequestContent() string {
	taskContent, _ := d.stateData["task_content"].(string)
	plan, _ := d.stateData["plan"].(string)

	content := fmt.Sprintf("Plan review requested. Please review:\n\nOriginal Task: %s\n\nProposed Plan: %s\n\nReady for architect approval.",
		taskContent, plan)

	// Mark that we've sent the request
	d.stateData["plan_review_request_sent"] = true

	return content
}

// hasCodeReviewRequest checks if a code review request has already been sent
func (d *Driver) hasCodeReviewRequest() bool {
	_, exists := d.stateData["code_review_request_sent"]
	return exists
}

// generateCodeReviewRequestContent creates content for the code review request
func (d *Driver) generateCodeReviewRequestContent() string {
	taskContent, _ := d.stateData["task_content"].(string)
	plan, _ := d.stateData["plan"].(string)
	implementation, _ := d.stateData["implementation"].(string)

	content := fmt.Sprintf("Code review requested. Please review:\n\nOriginal Task: %s\n\nPlan: %s\n\nImplementation: %s\n\nCode generation: completed\nTests: passed\n\nReady for architect approval.",
		taskContent, plan, truncateString(implementation, 200))

	// Mark that we've sent the request
	d.stateData["code_review_request_sent"] = true

	return content
}

// hasAnsweredCodingQuestion checks if we've already answered a coding question to avoid infinite loops
func (d *Driver) hasAnsweredCodingQuestion() bool {
	_, exists := d.stateData["coding_question_answered"]
	return exists
}

// containsKeyword checks if text contains a keyword (case-insensitive)
func containsKeyword(text, keyword string) bool {
	// Simple case-insensitive search
	// Could be enhanced with more sophisticated matching
	text = fmt.Sprintf(" %s ", text) // Add spaces for word boundary matching
	keyword = fmt.Sprintf(" %s ", keyword)

	for i := 0; i <= len(text)-len(keyword); i++ {
		match := true
		for j := 0; j < len(keyword); j++ {
			c1 := text[i+j]
			c2 := keyword[j]

			// Convert to lowercase for comparison
			if c1 >= 'A' && c1 <= 'Z' {
				c1 = c1 + 32
			}
			if c2 >= 'A' && c2 <= 'Z' {
				c2 = c2 + 32
			}

			if c1 != c2 {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}

	return false
}

// detectHelpRequest checks if the task content contains help requests
func detectHelpRequest(taskContent string) bool {
	lowerContent := strings.ToLower(taskContent)
	helpKeywords := []string{
		"get_help", "help", "question", "clarify", "guidance",
		"not sure", "unclear", "architect", "ask",
	}

	for _, keyword := range helpKeywords {
		if strings.Contains(lowerContent, keyword) {
			return true
		}
	}

	// Also check for <tool name="get_help"> pattern
	return strings.Contains(lowerContent, `<tool name="get_help">`) ||
		strings.Contains(lowerContent, `tool name="get_help"`)
}

// formatContextAsString formats the context messages as a string for LLM prompts
func (d *Driver) formatContextAsString() string {
	messages := d.contextManager.GetMessages()
	if len(messages) == 0 {
		return "No previous context"
	}

	var contextParts []string
	for _, msg := range messages {
		contextParts = append(contextParts, fmt.Sprintf("%s: %s", msg.Role, msg.Content))
	}

	return strings.Join(contextParts, "\n")
}

// parsePlanningResponse extracts plan and next action from LLM response
func (d *Driver) parsePlanningResponse(response string) (plan string, nextAction string) {
	// Try to extract JSON from the response
	// Look for common patterns in the response

	// Default values
	plan = "LLM generated plan"
	nextAction = "TOOL_INVOCATION"

	// Simple parsing - look for next_action field
	if strings.Contains(strings.ToLower(response), `"next_action": "coding"`) ||
		strings.Contains(strings.ToLower(response), `next_action.*coding`) {
		nextAction = "CODING"
	} else if strings.Contains(strings.ToLower(response), `"next_action": "tool_invocation"`) ||
		strings.Contains(strings.ToLower(response), `next_action.*tool`) {
		nextAction = "TOOL_INVOCATION"
	}

	// Try to extract plan text (simple approach)
	if strings.Contains(response, `"analysis":`) {
		// Try to find analysis content
		lines := strings.Split(response, "\n")
		for _, line := range lines {
			if strings.Contains(line, `"analysis":`) && len(line) > 20 {
				plan = strings.TrimSpace(line)
				break
			}
		}
	}

	// If we can't parse, use the whole response as plan
	if plan == "LLM generated plan" && len(response) > 0 {
		// Use first few sentences as plan
		sentences := strings.Split(response, ".")
		if len(sentences) > 0 {
			plan = strings.TrimSpace(sentences[0])
			if len(sentences) > 1 {
				plan += ". " + strings.TrimSpace(sentences[1])
			}
		}
	}

	return plan, nextAction
}

// executeToolCall executes a single MCP tool call
func (d *Driver) executeToolCall(ctx context.Context, call tools.ToolCall) (map[string]interface{}, error) {
	// Get the tool from the registry
	tool, err := tools.Get(call.Name)
	if err != nil {
		return nil, fmt.Errorf("tool %s not found in registry: %w", call.Name, err)
	}

	// Execute the tool with the parsed arguments
	result, err := tool.Exec(ctx, call.Args)
	if err != nil {
		return nil, fmt.Errorf("tool %s execution failed: %w", call.Name, err)
	}

	// Log execution for tracing
	if stdout, ok := result["stdout"].(string); ok && len(stdout) > 0 {
		fmt.Printf("DEBUG: Tool %s output: %s\n", call.Name, truncateString(stdout, 100))
	}

	return result, nil
}

// truncateString truncates a string to maxLen characters for logging
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}