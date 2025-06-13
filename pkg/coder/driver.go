package coder

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/state"
	"orchestrator/pkg/templates"
)

// State data keys - using constants to prevent key mismatch bugs
const (
	keyPlanApprovalResult = "plan_approval_result"
	keyCodeApprovalResult = "code_approval_result"
	keyArchitectAnswer    = "architect_answer"
	keyTaskContent        = "task_content"
	keyStartedAt          = "started_at"
)

// CoderDriver implements the v2 FSM using agent foundation
type CoderDriver struct {
	*agent.BaseStateMachine // Directly embed state machine
	config         *agent.AgentConfig
	contextManager *contextmgr.ContextManager
	llmClient      agent.LLMClient
	renderer       *templates.Renderer
	workDir        string
	
	// REQUEST→RESULT flow state
	pendingApprovalRequest *ApprovalRequest
	pendingQuestion        *Question
}

// ApprovalRequest represents a pending approval request
type ApprovalRequest struct {
	Content string
	Reason  string
	Type    string // "plan" or "code"
}

// ApprovalResult represents the result of an approval request
type ApprovalResult struct {
	Type   string    `json:"type"`   // "plan" or "code"
	Status string    `json:"status"` // "APPROVED", "REJECTED", "NEEDS_CHANGES"
	Time   time.Time `json:"time"`
}

// Question represents a pending question
type Question struct {
	Content string
	Reason  string
	Origin  string
}


// NewCoderDriver creates a new coder driver using agent foundation
func NewCoderDriver(agentID string, stateStore *state.Store, modelConfig *config.ModelCfg, llmClient agent.LLMClient, workDir string) (*CoderDriver, error) {
	renderer, _ := templates.NewRenderer()
	
	// Create agent context with logger
	agentCtx := &agent.AgentContext{
		Context: context.Background(),
		Logger:  log.New(os.Stdout, fmt.Sprintf("[%s] ", agentID), log.LstdFlags),
		Store:   stateStore,
		WorkDir: workDir,
	}
	
	// Create agent config
	agentConfig := &agent.AgentConfig{
		ID:      agentID,
		Type:    "coder",
		Context: *agentCtx,
		LLMConfig: &agent.LLMConfig{
			MaxContextTokens: modelConfig.MaxContextTokens,
			MaxOutputTokens:  modelConfig.MaxReplyTokens,
			CompactIfOver:   modelConfig.CompactionBuffer,
		},
	}
	
	// Create base state machine directly
	sm := agent.NewBaseStateMachine(agentID, agent.StateWaiting, stateStore)
	
	return &CoderDriver{
		BaseStateMachine: sm,
		config:          agentConfig,
		contextManager:  contextmgr.NewContextManagerWithModel(modelConfig),
		llmClient:       llmClient,
		renderer:        renderer,
		workDir:         workDir,
	}, nil
}

// ProcessState implements the v2 FSM state machine logic
func (d *CoderDriver) ProcessState(ctx context.Context) (agent.State, bool, error) {
	sm := d.BaseStateMachine
	
	switch d.GetCurrentState() {
	case agent.StateWaiting:
		return d.handleWaiting(ctx, sm)
	case agent.StatePlanning:
		return d.handlePlanning(ctx, sm)
	case agent.StatePlanReview:
		return d.handlePlanReview(ctx, sm)
	case agent.StateCoding:
		return d.handleCoding(ctx, sm)
	case agent.StateTesting:
		return d.handleTesting(ctx, sm)
	case agent.StateFixing:
		return d.handleFixing(ctx, sm)
	case agent.StateCodeReview:
		return d.handleCodeReview(ctx, sm)
	case agent.StateQuestion:
		return d.handleQuestion(ctx, sm)
	case agent.StateDone:
		return agent.StateDone, true, nil
	case agent.StateError:
		return agent.StateError, true, nil
	default:
		return agent.StateError, false, fmt.Errorf("unknown state: %s", d.GetCurrentState())
	}
}

// ProcessTask initiates task processing with the new agent foundation
func (d *CoderDriver) ProcessTask(ctx context.Context, taskContent string) error {
	// Reset for new task
	d.BaseStateMachine.SetStateData(keyTaskContent, taskContent)
	d.BaseStateMachine.SetStateData(keyStartedAt, time.Now().UTC())
	
	// Add to context manager
	d.contextManager.AddMessage("user", taskContent)
	
	// Initialize if needed
	if err := d.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize: %w", err)
	}
	
	// Run the state machine loop
	for {
		nextState, done, err := d.ProcessState(ctx)
		if err != nil {
			return err
		}
		
		// Transition to next state if different (even if done=true)
		currentState := d.GetCurrentState()
		if nextState != currentState {
			if err := d.TransitionTo(ctx, nextState, nil); err != nil {
				return fmt.Errorf("failed to transition to state %s: %w", nextState, err)
			}
		}
		
		if done {
			break
		}
	}
	
	return nil
}



// handleWaiting processes the WAITING state
func (d *CoderDriver) handleWaiting(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Waiting for task assignment")
	
	taskContent, exists := sm.GetStateValue(keyTaskContent)
	if exists && taskContent != "" {
		return agent.StatePlanning, false, nil
	}
	
	return agent.StateWaiting, false, nil
}

// handlePlanning processes the PLANNING state
func (d *CoderDriver) handlePlanning(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Planning phase: analyzing requirements")
	
	taskContent, _ := sm.GetStateValue(keyTaskContent)
	taskStr, _ := taskContent.(string)
	
	// Check for help requests
	if d.detectHelpRequest(taskStr) {
		sm.SetStateData("question_reason", "Help requested during planning")
		sm.SetStateData("question_content", taskStr)
		sm.SetStateData("question_origin", "PLANNING")
		return agent.StateQuestion, false, nil
	}
	
	// Generate plan
	if d.llmClient != nil {
		return d.handlePlanningWithLLM(ctx, sm, taskStr)
	}
	
	// Mock mode
	sm.SetStateData("plan", "Mock plan: Analyzed requirements, ready to proceed")
	sm.SetStateData("planning_completed_at", time.Now().UTC())
	return agent.StatePlanReview, false, nil
}

// handlePlanningWithLLM generates plan using LLM
func (d *CoderDriver) handlePlanningWithLLM(ctx context.Context, sm *agent.BaseStateMachine, taskContent string) (agent.State, bool, error) {
	// Create planning prompt
	templateData := &templates.TemplateData{
		TaskContent: taskContent,
		Context:     d.formatContextAsString(),
	}
	
	prompt, err := d.renderer.Render(templates.PlanningTemplate, templateData)
	if err != nil {
		return agent.StateError, false, fmt.Errorf("failed to render planning template: %w", err)
	}
	
	// Get LLM response
	req := agent.CompletionRequest{
		Messages: []agent.CompletionMessage{
			{Role: agent.RoleUser, Content: prompt},
		},
		MaxTokens: 4096,
	}
	
	resp, err := d.llmClient.Complete(ctx, req)
	if err != nil {
		return agent.StateError, false, fmt.Errorf("failed to get LLM planning response: %w", err)
	}
	
	// Store plan
	sm.SetStateData("plan", resp.Content)
	sm.SetStateData("planning_completed_at", time.Now().UTC())
	d.contextManager.AddMessage("assistant", resp.Content)
	
	return agent.StatePlanReview, false, nil
}

// handlePlanReview processes the PLAN_REVIEW state using REQUEST→RESULT flow
func (d *CoderDriver) handlePlanReview(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Plan review phase: requesting architect approval")
	
	// Check if we already have approval result
	if approvalData, exists := sm.GetStateValue(keyPlanApprovalResult); exists {
		if result, ok := approvalData.(*ApprovalResult); ok {
			sm.SetStateData("plan_review_completed_at", time.Now().UTC())
			
			switch result.Status {
			case "APPROVED":
				return agent.StateCoding, false, nil
			case "REJECTED", "NEEDS_CHANGES":
				return agent.StatePlanning, false, nil
			default:
				return agent.StateError, false, fmt.Errorf("unknown approval status: %s", result.Status)
			}
		}
	}
	
	// In mock mode (no LLM client), auto-approve
	if d.llmClient == nil {
		taskContent, _ := sm.GetStateValue(keyTaskContent)
		taskStr, _ := taskContent.(string)
		
		approved := d.simulateApproval(taskStr, "plan")
		result := &ApprovalResult{
			Type:   "plan",
			Status: "APPROVED",
			Time:   time.Now().UTC(),
		}
		if !approved {
			result.Status = "REJECTED"
		}
		
		sm.SetStateData(keyPlanApprovalResult, result)
		sm.SetStateData("plan_review_completed_at", time.Now().UTC())
		
		if approved {
			return agent.StateCoding, false, nil
		}
		return agent.StatePlanning, false, nil
	}
	
	// Create approval request for architect (LLM mode)
	plan, _ := sm.GetStateValue("plan")
	planStr, _ := plan.(string)
	
	d.pendingApprovalRequest = &ApprovalRequest{
		Content: planStr,
		Reason:  "Plan requires architect approval before proceeding to coding",
		Type:    "plan",
	}
	
	// Stay in PLAN_REVIEW until we get approval result
	return agent.StatePlanReview, false, nil
}

// handleCoding processes the CODING state
func (d *CoderDriver) handleCoding(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Coding phase: implementing solution")
	
	// Mock implementation for now
	sm.SetStateData("code_generated", true)
	sm.SetStateData("coding_completed_at", time.Now().UTC())
	
	return agent.StateTesting, false, nil
}

// handleTesting processes the TESTING state
func (d *CoderDriver) handleTesting(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Testing phase: running tests")
	
	// Check for deliberate test failures
	taskContent, _ := sm.GetStateValue(keyTaskContent)
	taskStr, _ := taskContent.(string)
	
	shouldFail := strings.Contains(strings.ToLower(taskStr), "test fail") ||
		strings.Contains(strings.ToLower(taskStr), "simulate fail")
		
	// Check if already tried fixing
	_, alreadyFixed := sm.GetStateValue("fixes_applied")
	
	testsPassed := !shouldFail || alreadyFixed
	sm.SetStateData("tests_passed", testsPassed)
	sm.SetStateData("testing_completed_at", time.Now().UTC())
	
	if !testsPassed {
		return agent.StateFixing, false, nil
	}
	
	return agent.StateCodeReview, false, nil
}

// handleFixing processes the FIXING state
func (d *CoderDriver) handleFixing(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Fixing phase: addressing issues")
	
	sm.SetStateData("fixes_applied", true)
	sm.SetStateData("fixing_completed_at", time.Now().UTC())
	
	return agent.StateCoding, false, nil
}

// handleCodeReview processes the CODE_REVIEW state using REQUEST→RESULT flow
func (d *CoderDriver) handleCodeReview(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Code review phase: requesting architect approval")
	
	// Check if we already have approval result
	if approvalData, exists := sm.GetStateValue(keyCodeApprovalResult); exists {
		if result, ok := approvalData.(*ApprovalResult); ok {
			sm.SetStateData("code_review_completed_at", time.Now().UTC())
			
			switch result.Status {
			case "APPROVED":
				return agent.StateDone, true, nil
			case "REJECTED", "NEEDS_CHANGES":
				return agent.StateFixing, false, nil
			default:
				return agent.StateError, false, fmt.Errorf("unknown approval status: %s", result.Status)
			}
		}
	}
	
	// In mock mode (no LLM client), auto-approve
	if d.llmClient == nil {
		taskContent, _ := sm.GetStateValue(keyTaskContent)
		taskStr, _ := taskContent.(string)
		
		approved := d.simulateApproval(taskStr, "code")
		result := &ApprovalResult{
			Type:   "code",
			Status: "APPROVED", 
			Time:   time.Now().UTC(),
		}
		if !approved {
			result.Status = "REJECTED"
		}
		
		sm.SetStateData(keyCodeApprovalResult, result)
		sm.SetStateData("code_review_completed_at", time.Now().UTC())
		
		if approved {
			return agent.StateDone, true, nil
		}
		return agent.StateFixing, false, nil
	}
	
	// Create approval request for architect (LLM mode)
	codeGenerated, _ := sm.GetStateValue("code_generated")
	codeStr := fmt.Sprintf("Code implementation completed: %v", codeGenerated)
	
	d.pendingApprovalRequest = &ApprovalRequest{
		Content: codeStr,
		Reason:  "Code requires architect approval before completion",
		Type:    "code",
	}
	
	// Stay in CODE_REVIEW until we get approval result
	return agent.StateCodeReview, false, nil
}

// handleQuestion processes the QUESTION state with origin tracking
func (d *CoderDriver) handleQuestion(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Question phase: awaiting clarification")
	
	// Check if we have an answer
	if answer, exists := sm.GetStateValue(keyArchitectAnswer); exists {
		answerStr, _ := answer.(string)
		sm.SetStateData("question_answered", true)
		sm.SetStateData("architect_response", answerStr)
		sm.SetStateData("question_completed_at", time.Now().UTC())
		
		// Clear the answer so we don't loop
		sm.SetStateData(keyArchitectAnswer, "")
		
		// Return to origin state using metadata
		origin, _ := sm.GetStateValue("question_origin")
		originStr, _ := origin.(string)
		
		switch originStr {
		case "PLANNING":
			return agent.StatePlanning, false, nil
		case "CODING":
			return agent.StateCoding, false, nil
		case "FIXING":
			return agent.StateFixing, false, nil
		default:
			return agent.StatePlanning, false, nil
		}
	}
	
	// Create question for architect if we don't have one pending
	if d.pendingQuestion == nil {
		questionContent, _ := sm.GetStateValue("question_content")
		questionReason, _ := sm.GetStateValue("question_reason")
		questionOrigin, _ := sm.GetStateValue("question_origin")
		
		d.pendingQuestion = &Question{
			Content: questionContent.(string),
			Reason:  questionReason.(string),
			Origin:  questionOrigin.(string),
		}
	}
	
	// Stay in QUESTION state until we get an answer
	return agent.StateQuestion, false, nil
}

// Helper methods

func (d *CoderDriver) detectHelpRequest(taskContent string) bool {
	lower := strings.ToLower(taskContent)
	helpKeywords := []string{"help", "question", "clarify", "guidance", "not sure", "unclear"}
	
	for _, keyword := range helpKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func (d *CoderDriver) simulateApproval(taskContent, reviewType string) bool {
	lower := strings.ToLower(taskContent)
	
	if strings.Contains(lower, "approve") || strings.Contains(lower, "looks good") {
		return true
	}
	if strings.Contains(lower, "change") || strings.Contains(lower, "fix") || strings.Contains(lower, "modify") {
		return false
	}
	
	// Default approval
	return true
}

func (d *CoderDriver) formatContextAsString() string {
	messages := d.contextManager.GetMessages()
	if len(messages) == 0 {
		return "No previous context"
	}
	
	var parts []string
	for _, msg := range messages {
		parts = append(parts, fmt.Sprintf("%s: %s", msg.Role, msg.Content))
	}
	
	return strings.Join(parts, "\n")
}

// GetPendingApprovalRequest returns pending approval request if any
func (d *CoderDriver) GetPendingApprovalRequest() (bool, string, string) {
	if d.pendingApprovalRequest == nil {
		return false, "", ""
	}
	return true, d.pendingApprovalRequest.Content, d.pendingApprovalRequest.Reason
}

// ClearPendingApprovalRequest clears the pending approval request
func (d *CoderDriver) ClearPendingApprovalRequest() {
	d.pendingApprovalRequest = nil
}

// GetPendingQuestion returns pending question if any
func (d *CoderDriver) GetPendingQuestion() (bool, string, string) {
	if d.pendingQuestion == nil {
		return false, "", ""
	}
	return true, d.pendingQuestion.Content, d.pendingQuestion.Reason
}

// ClearPendingQuestion clears the pending question
func (d *CoderDriver) ClearPendingQuestion() {
	d.pendingQuestion = nil
}

// ProcessApprovalResult processes approval result from architect
func (d *CoderDriver) ProcessApprovalResult(approvalStatus string, approvalType string) error {
	result := &ApprovalResult{
		Type:   approvalType,
		Status: approvalStatus,
		Time:   time.Now().UTC(),
	}
	
	// Store using the correct key based on type
	switch approvalType {
	case "plan":
		d.BaseStateMachine.SetStateData(keyPlanApprovalResult, result)
	case "code":
		d.BaseStateMachine.SetStateData(keyCodeApprovalResult, result)
	default:
		return fmt.Errorf("unknown approval type: %s", approvalType)
	}
	
	return nil
}

// ProcessAnswer processes answer from architect
func (d *CoderDriver) ProcessAnswer(answer string) error {
	d.BaseStateMachine.SetStateData(keyArchitectAnswer, answer)
	return nil
}

// GetContextSummary returns a summary of the current context
func (d *CoderDriver) GetContextSummary() string {
	messages := d.contextManager.GetMessages()
	if len(messages) == 0 {
		return "No context available"
	}
	
	// Return a summary of the last few messages
	summary := fmt.Sprintf("Context summary: %d messages", len(messages))
	if len(messages) > 0 {
		lastMsg := messages[len(messages)-1]
		summary += fmt.Sprintf(", last: %s: %s", lastMsg.Role, lastMsg.Content)
	}
	
	return summary
}

// GetStateData returns the current state data
func (d *CoderDriver) GetStateData() map[string]interface{} {
	return d.BaseStateMachine.GetStateData()
}

// Run executes the driver's main loop (required for Driver interface)
func (d *CoderDriver) Run(ctx context.Context) error {
	// Initialize if needed
	if err := d.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize: %w", err)
	}
	
	// Run the state machine loop
	for {
		nextState, done, err := d.ProcessState(ctx)
		if err != nil {
			return err
		}
		
		// Transition to next state if different (even if done=true)
		currentState := d.GetCurrentState()
		if nextState != currentState {
			if err := d.TransitionTo(ctx, nextState, nil); err != nil {
				return fmt.Errorf("failed to transition to state %s: %w", nextState, err)
			}
		}
		
		if done {
			break
		}
	}
	
	return nil
}

// Step executes a single step (required for Driver interface)
func (d *CoderDriver) Step(ctx context.Context) (bool, error) {
	nextState, done, err := d.ProcessState(ctx)
	if err != nil {
		return false, err
	}
	
	if done {
		return true, nil
	}
	
	// Transition to next state if different
	currentState := d.GetCurrentState()
	if nextState != currentState {
		if err := d.TransitionTo(ctx, nextState, nil); err != nil {
			return false, fmt.Errorf("failed to transition to state %s: %w", nextState, err)
		}
	}
	
	return false, nil
}

// Shutdown performs cleanup (required for Driver interface)
func (d *CoderDriver) Shutdown(ctx context.Context) error {
	return d.Persist()
}

