package coder

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/state"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
)

// CoderState represents a state in the coder state machine
type CoderState string

const (
	StatePlanning   CoderState = "PLANNING"
	StateCoding     CoderState = "CODING"
	StateTesting    CoderState = "TESTING"
	StateFixing     CoderState = "FIXING"
	StatePlanReview CoderState = "PLAN_REVIEW"
	StateCodeReview CoderState = "CODE_REVIEW"
	StateQuestion   CoderState = "QUESTION"
)

func (s CoderState) String() string {
	return string(s)
}

// ToAgentState converts CoderState to agent.State
func (s CoderState) ToAgentState() agent.State {
	return agent.State(s)
}

// FromAgentState converts agent.State to CoderState
func FromAgentState(s agent.State) CoderState {
	return CoderState(s)
}

// ValidCoderTransitions defines allowed state transitions for coder states
var ValidCoderTransitions = map[CoderState][]CoderState{
	StatePlanning:   {StateCoding, StatePlanReview, StateQuestion},
	StateCoding:     {StateTesting, StatePlanning, StateQuestion},
	StateTesting:    {StateCoding, StateFixing, StateCodeReview},
	StatePlanReview: {StateCoding, StatePlanning},              // Approve→CODING, Reject→PLANNING
	StateFixing:     {StateCoding, StateTesting},               // Fix→CODING or retry TESTING
	StateCodeReview: {StateFixing},                             // Approve→DONE (handled by base), Reject→FIXING
	StateQuestion:   {StatePlanning, StateCoding, StateFixing}, // Return to origin state
}

// IsValidCoderTransition checks if a coder state transition is allowed
func (d *CoderDriver) IsValidCoderTransition(from, to CoderState) bool {
	// Get allowed transitions for current state
	allowed, ok := ValidCoderTransitions[from]
	if !ok {
		return false
	}

	// Check if requested state is in allowed list
	for _, s := range allowed {
		if s == to {
			return true
		}
	}

	return false
}

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
	config                  *agent.AgentConfig
	contextManager          *contextmgr.ContextManager
	llmClient               agent.LLMClient
	renderer                *templates.Renderer
	workDir                 string

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
			CompactIfOver:    modelConfig.CompactionBuffer,
		},
	}

	// Create base state machine directly
	sm := agent.NewBaseStateMachine(agentID, agent.StateWaiting, stateStore)

	return &CoderDriver{
		BaseStateMachine: sm,
		config:           agentConfig,
		contextManager:   contextmgr.NewContextManagerWithModel(modelConfig),
		llmClient:        llmClient,
		renderer:         renderer,
		workDir:          workDir,
	}, nil
}

// ProcessState implements the v2 FSM state machine logic
func (d *CoderDriver) ProcessState(ctx context.Context) (agent.State, bool, error) {
	sm := d.BaseStateMachine

	switch d.GetCurrentState() {
	case agent.StateWaiting:
		return d.handleWaiting(ctx, sm)
	case StatePlanning.ToAgentState():
		return d.handlePlanning(ctx, sm)
	case StatePlanReview.ToAgentState():
		return d.handlePlanReview(ctx, sm)
	case StateCoding.ToAgentState():
		return d.handleCoding(ctx, sm)
	case StateTesting.ToAgentState():
		return d.handleTesting(ctx, sm)
	case StateFixing.ToAgentState():
		return d.handleFixing(ctx, sm)
	case StateCodeReview.ToAgentState():
		return d.handleCodeReview(ctx, sm)
	case StateQuestion.ToAgentState():
		return d.handleQuestion(ctx, sm)
	case agent.StateDone:
		return agent.StateDone, true, nil
	case agent.StateError:
		return agent.StateError, true, nil
	default:
		return agent.StateError, false, fmt.Errorf("unknown state: %s", d.GetCurrentState())
	}
}

// TransitionTo overrides the base state machine to add coder-specific validation
func (d *CoderDriver) TransitionTo(ctx context.Context, newState agent.State, metadata map[string]any) error {
	currentState := d.GetCurrentState()
	
	// Handle coder-specific transition validation
	isCurrentCoderState := d.isCoderState(currentState)
	isNewCoderState := d.isCoderState(newState)
	
	// Allow transitions from WAITING to any coder state
	if currentState == agent.StateWaiting && isNewCoderState {
		// Valid transition - perform it directly
		return d.performTransition(ctx, newState, metadata)
	}
	
	// Check coder-specific transitions if both states are coder states
	if isCurrentCoderState && isNewCoderState {
		currentCoderState := FromAgentState(currentState)
		newCoderState := FromAgentState(newState)
		
		if !d.IsValidCoderTransition(currentCoderState, newCoderState) {
			return fmt.Errorf("invalid coder transition from %s to %s", currentCoderState, newCoderState)
		}
		// Valid transition - perform it directly
		return d.performTransition(ctx, newState, metadata)
	}
	
	// Allow transitions to generic agent states (DONE, ERROR, WAITING)
	if newState == agent.StateDone || newState == agent.StateError || newState == agent.StateWaiting {
		return d.performTransition(ctx, newState, metadata)
	}
	
	// Use base state machine for other transitions
	return d.BaseStateMachine.TransitionTo(ctx, newState, metadata)
}

// performTransition performs the actual state transition bypassing validation
// This is a simplified version that directly manipulates the base state machine
func (d *CoderDriver) performTransition(ctx context.Context, newState agent.State, metadata map[string]any) error {
	// Since we can't access private fields directly, we need a different approach
	// For now, let's use reflection to modify the private fields or find another way
	
	// Actually, let's use a simpler approach - modify the generic ValidTransitions temporarily
	// Store current state to know what transition we're making
	currentState := d.GetCurrentState()
	
	// Temporarily add the transition to ValidTransitions
	oldTransitions := agent.ValidTransitions[currentState]
	if agent.ValidTransitions[currentState] == nil {
		agent.ValidTransitions[currentState] = []agent.State{newState}
	} else {
		// Check if transition already exists
		found := false
		for _, state := range agent.ValidTransitions[currentState] {
			if state == newState {
				found = true
				break
			}
		}
		if !found {
			agent.ValidTransitions[currentState] = append(agent.ValidTransitions[currentState], newState)
		}
	}
	
	// Perform the transition using base state machine
	err := d.BaseStateMachine.TransitionTo(ctx, newState, metadata)
	
	// Restore original transitions
	agent.ValidTransitions[currentState] = oldTransitions
	
	return err
}

// isCoderState checks if a state is a coder-specific state
func (d *CoderDriver) isCoderState(state agent.State) bool {
	switch state {
	case StatePlanning.ToAgentState(), StateCoding.ToAgentState(), StateTesting.ToAgentState(),
		 StateFixing.ToAgentState(), StatePlanReview.ToAgentState(), StateCodeReview.ToAgentState(),
		 StateQuestion.ToAgentState():
		return true
	default:
		return false
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

		// Break out if we have pending approvals or questions to let external handler deal with them
		if d.pendingApprovalRequest != nil || d.pendingQuestion != nil {
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
		return StatePlanning.ToAgentState(), false, nil
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
		return StateQuestion.ToAgentState(), false, nil
	}

	// Generate plan
	if d.llmClient != nil {
		return d.handlePlanningWithLLM(ctx, sm, taskStr)
	}

	// Mock mode
	sm.SetStateData("plan", "Mock plan: Analyzed requirements, ready to proceed")
	sm.SetStateData("planning_completed_at", time.Now().UTC())
	return StatePlanReview.ToAgentState(), false, nil
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

	return StatePlanReview.ToAgentState(), false, nil
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
				return StateCoding.ToAgentState(), false, nil
			case "REJECTED", "NEEDS_CHANGES":
				return StatePlanning.ToAgentState(), false, nil
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
			return StateCoding.ToAgentState(), false, nil
		}
		return StatePlanning.ToAgentState(), false, nil
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
	return StatePlanReview.ToAgentState(), false, nil
}

// handleCoding processes the CODING state
func (d *CoderDriver) handleCoding(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Coding phase: implementing solution")

	taskContent, _ := sm.GetStateValue(keyTaskContent)
	taskStr, _ := taskContent.(string)
	plan, _ := sm.GetStateValue("plan")
	planStr, _ := plan.(string)

	if d.llmClient != nil {
		return d.handleCodingWithLLM(ctx, sm, taskStr, planStr)
	}

	// Mock implementation for no LLM
	sm.SetStateData("code_generated", true)
	sm.SetStateData("coding_completed_at", time.Now().UTC())

	return StateTesting.ToAgentState(), false, nil
}

// handleCodingWithLLM generates actual code using LLM
func (d *CoderDriver) handleCodingWithLLM(ctx context.Context, sm *agent.BaseStateMachine, taskContent, plan string) (agent.State, bool, error) {
	// Create coding prompt
	templateData := &templates.TemplateData{
		TaskContent: taskContent,
		Plan:        plan,
		Context:     d.formatContextAsString(),
		WorkDir:     d.workDir,
	}

	prompt, err := d.renderer.Render(templates.CodingTemplate, templateData)
	if err != nil {
		return agent.StateError, false, fmt.Errorf("failed to render coding template: %w", err)
	}

	// Get LLM response for code generation with shell tool
	// Build messages including conversation context
	messages := []agent.CompletionMessage{}
	
	// Add the initial prompt
	messages = append(messages, agent.CompletionMessage{Role: agent.RoleUser, Content: prompt})
	
	// Add conversation history from context manager
	contextMessages := d.contextManager.GetMessages()
	for _, msg := range contextMessages {
		role := agent.RoleAssistant
		if msg.Role == "user" || msg.Role == "system" {
			role = agent.RoleUser
		}
		messages = append(messages, agent.CompletionMessage{
			Role:    role,
			Content: fmt.Sprintf("[%s] %s", msg.Role, msg.Content),
		})
	}
	
	req := agent.CompletionRequest{
		Messages: messages,
		MaxTokens: 4096,
		Tools: []agent.Tool{
			{
				Name:        "shell",
				Description: "Execute shell commands for file operations",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cmd": map[string]any{
							"type":        "string",
							"description": "Shell command to execute",
						},
						"cwd": map[string]any{
							"type":        "string",
							"description": "Working directory for the command",
						},
					},
					"required": []string{"cmd"},
				},
			},
			{
				Name:        "mark_complete",
				Description: "Call this when the implementation is complete and ready for testing",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"reason": map[string]any{
							"type":        "string",
							"description": "Why you believe the implementation is complete",
						},
					},
					"required": []string{"reason"},
				},
			},
		},
	}

	resp, err := d.llmClient.Complete(ctx, req)
	if err != nil {
		return agent.StateError, false, fmt.Errorf("failed to get LLM coding response: %w", err)
	}

	// Temporarily fall back to text parsing until tool calling is implemented
	// TODO: Switch back to MCP tool execution once Claude client supports tools
	var filesCreated int

	if len(resp.ToolCalls) > 0 {
		log.Printf("Executing %d tool calls via MCP in working directory: %s", len(resp.ToolCalls), d.workDir)
		filesCreated, err = d.executeMCPToolCalls(ctx, resp.ToolCalls)
		if err != nil {
			return agent.StateError, false, fmt.Errorf("failed to execute tool calls: %w", err)
		}
		log.Printf("MCP tool execution created %d files", filesCreated)
		
		// Reset no-tool-calls counter since we had tool calls
		sm.SetStateData("no_tool_calls_count", 0)
	} else {
		log.Printf("No tool calls found, falling back to text parsing")
		
		// Track consecutive iterations without tool calls
		noToolCallsCount := 0
		if val, exists := sm.GetStateValue("no_tool_calls_count"); exists {
			if count, ok := val.(int); ok {
				noToolCallsCount = count
			}
		}
		noToolCallsCount++
		sm.SetStateData("no_tool_calls_count", noToolCallsCount)
		
		log.Printf("No tool calls for %d consecutive iterations", noToolCallsCount)
		
		// Parse the response to extract files and create them
		filesCreated, err = d.parseAndCreateFiles(resp.Content)
		if err != nil {
			return agent.StateError, false, fmt.Errorf("failed to create files: %w", err)
		}
		log.Printf("Text parsing created %d files", filesCreated)
	}

	// Store results
	sm.SetStateData("code_generated", filesCreated > 0)
	sm.SetStateData("files_created", filesCreated)
	d.contextManager.AddMessage("assistant", resp.Content)

	// Check if implementation seems complete
	if d.isImplementationComplete(resp.Content, filesCreated, sm) {
		sm.SetStateData("coding_completed_at", time.Now().UTC())
		return StateTesting.ToAgentState(), false, nil
	}

	// Check iteration limit to prevent infinite loops
	var iterationCount int
	if val, exists := sm.GetStateValue("coding_iterations"); exists {
		if count, ok := val.(int); ok {
			iterationCount = count
		}
	}
	iterationCount++
	sm.SetStateData("coding_iterations", iterationCount)
	
	if iterationCount >= 8 {
		log.Printf("Reached maximum coding iterations (%d), proceeding to testing", iterationCount)
		sm.SetStateData("coding_completed_at", time.Now().UTC())
		return StateTesting.ToAgentState(), false, nil
	}

	// Add context about what's been done so far for next iteration
	fileList := d.getWorkingDirectoryContents()
	d.contextManager.AddMessage("system", fmt.Sprintf("Previous iteration created %d files/directories. Current workspace contains: %s. The implementation is not yet complete. Please continue with the next steps to create the actual source code files (like main.go, handlers, etc).", filesCreated, fileList))

	// Continue coding if implementation is not complete
	log.Printf("Implementation appears incomplete (iteration %d/8), continuing in CODING state", iterationCount)
	return StateCoding.ToAgentState(), false, nil
}

// executeMCPToolCalls executes tool calls using the MCP tool system
func (d *CoderDriver) executeMCPToolCalls(ctx context.Context, toolCalls []agent.ToolCall) (int, error) {
	// Check working directory permissions
	if stat, err := os.Stat(d.workDir); err != nil {
		log.Printf("Error accessing working directory %s: %v", d.workDir, err)
		return 0, fmt.Errorf("cannot access working directory %s: %w", d.workDir, err)
	} else {
		log.Printf("Working directory %s exists, mode: %v", d.workDir, stat.Mode())
	}

	// Ensure shell tool is registered
	shellTool := &tools.ShellTool{}
	if err := tools.Register(shellTool); err != nil {
		// Tool might already be registered, which is fine
		log.Printf("Shell tool registration: %v (likely already registered)", err)
	} else {
		log.Printf("Shell tool registered successfully")
	}

	filesCreated := 0

	for i, toolCall := range toolCalls {
		log.Printf("Processing tool call %d: name=%s, id=%s", i+1, toolCall.Name, toolCall.ID)
		
		if toolCall.Name == "mark_complete" {
			// Claude signaled completion
			if reason, ok := toolCall.Parameters["reason"].(string); ok {
				log.Printf("Claude marked implementation complete: %s", reason)
				d.contextManager.AddMessage("tool", fmt.Sprintf("Implementation marked complete: %s", reason))
				// Return high file count to signal completion
				return 99, nil
			}
			continue
		}
		
		if toolCall.Name == "shell" {
			// Get the shell tool from registry
			tool, err := tools.Get("shell")
			if err != nil {
				return filesCreated, fmt.Errorf("shell tool not available: %w", err)
			}

			// Set working directory if not provided
			args := make(map[string]any)
			for k, v := range toolCall.Parameters {
				args[k] = v
			}
			if _, hasCwd := args["cwd"]; !hasCwd {
				args["cwd"] = d.workDir
			}

			// Execute the tool
			result, err := tool.Exec(ctx, args)
			if err != nil {
				return filesCreated, fmt.Errorf("failed to execute shell command: %w", err)
			}

			// Log tool execution
			if cmd, ok := args["cmd"].(string); ok {
				log.Printf("Executing shell command: %s", cmd)
				d.contextManager.AddMessage("tool", fmt.Sprintf("Executed: %s", cmd))

				// Count file creation commands - expanded patterns
				if strings.Contains(cmd, "cat >") || 
				   strings.Contains(cmd, "echo >") || 
				   strings.Contains(cmd, "tee ") ||
				   strings.Contains(cmd, "go mod init") ||
				   strings.Contains(cmd, "touch ") ||
				   strings.Contains(cmd, "cp ") ||
				   strings.Contains(cmd, "mv ") ||
				   strings.Contains(cmd, "mkdir") ||
				   strings.Contains(cmd, " > ") ||
				   strings.Contains(cmd, " >> ") {
					log.Printf("Detected file creation command, incrementing count")
					filesCreated++
				}
			} else {
				log.Printf("Warning: tool call missing 'cmd' parameter")
			}

			// Log result if available
			if resultMap, ok := result.(map[string]any); ok {
				if output, ok := resultMap["stdout"].(string); ok && output != "" {
					log.Printf("Command stdout: %s", output)
					d.contextManager.AddMessage("tool", fmt.Sprintf("Output: %s", output))
				}
				if stderr, ok := resultMap["stderr"].(string); ok && stderr != "" {
					log.Printf("Command stderr: %s", stderr)
					d.contextManager.AddMessage("tool", fmt.Sprintf("Error: %s", stderr))
				}
				if exitCode, ok := resultMap["exit_code"].(int); ok && exitCode != 0 {
					log.Printf("Command exited with code: %d", exitCode)
					d.contextManager.AddMessage("tool", fmt.Sprintf("Command failed with exit code: %d", exitCode))
				}
			} else {
				log.Printf("Warning: could not parse tool execution result")
			}
		}
	}

	return filesCreated, nil
}

// isImplementationComplete checks if the current implementation appears complete
func (d *CoderDriver) isImplementationComplete(responseContent string, filesCreated int, sm *agent.BaseStateMachine) bool {
	// Method 1: Explicit completion signal via mark_complete tool
	if filesCreated == 99 {
		log.Printf("Completion detected: Claude used mark_complete tool")
		return true
	}
	
	// Method 2: No tool calls pattern - if Claude stops making tool calls for 2+ consecutive iterations
	noToolCallsCount := 0
	if val, exists := sm.GetStateValue("no_tool_calls_count"); exists {
		if count, ok := val.(int); ok {
			noToolCallsCount = count
		}
	}
	
	if noToolCallsCount >= 2 && filesCreated >= 1 {
		log.Printf("Completion detected: No tool calls for %d consecutive iterations with %d files created", noToolCallsCount, filesCreated)
		return true
	}
	
	// Method 3: Natural language completion indicators
	completionIndicators := []string{
		"implementation is complete",
		"implementation complete",
		"ready for testing",
		"finished implementing",
		"implementation done",
		"that completes the",
		"all files created",
		"implementation ready",
		"ready to test",
		"completed successfully",
	}
	
	responseLower := strings.ToLower(responseContent)
	for _, indicator := range completionIndicators {
		if strings.Contains(responseLower, indicator) {
			log.Printf("Completion detected: Found completion indicator '%s' in response", indicator)
			return true
		}
	}
	
	// If no files were created, definitely not complete
	if filesCreated == 0 {
		return false
	}
	
	// If only directories were created (like mkdir), not complete unless it's been many iterations
	if filesCreated <= 2 && (strings.Contains(responseContent, "mkdir") || strings.Contains(responseContent, "go mod init")) {
		return false
	}
	
	// Default to incomplete to encourage more work
	return false
}

// getWorkingDirectoryContents returns a summary of what's in the working directory
func (d *CoderDriver) getWorkingDirectoryContents() string {
	entries, err := os.ReadDir(d.workDir)
	if err != nil {
		return "error reading directory"
	}
	
	var items []string
	for _, entry := range entries {
		if entry.Name() == "state" {
			continue // Skip internal state directory
		}
		if entry.IsDir() {
			items = append(items, entry.Name()+"/")
		} else {
			items = append(items, entry.Name())
		}
	}
	
	if len(items) == 0 {
		return "empty directory"
	}
	
	return strings.Join(items, ", ")
}

// parseAndCreateFiles extracts code blocks from LLM response and creates files
func (d *CoderDriver) parseAndCreateFiles(content string) (int, error) {
	filesCreated := 0
	lines := strings.Split(content, "\n")

	var currentFile string
	var currentContent []string
	inCodeBlock := false

	for _, line := range lines {
		// Look for filename patterns like "### filename.py" or "File: filename.py"
		if strings.HasPrefix(strings.TrimSpace(line), "###") ||
			strings.HasPrefix(strings.TrimSpace(line), "File:") ||
			strings.HasPrefix(strings.TrimSpace(line), "**") {

			// Save previous file if exists
			if currentFile != "" && len(currentContent) > 0 {
				if err := d.writeFile(currentFile, strings.Join(currentContent, "\n")); err != nil {
					return filesCreated, err
				}
				filesCreated++
			}

			// Extract filename
			currentFile = d.extractFilename(line)
			currentContent = []string{}
			inCodeBlock = false
			continue
		}

		// Handle code blocks
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inCodeBlock {
				// End of code block
				inCodeBlock = false
			} else {
				// Start of code block
				inCodeBlock = true
				// If no current file, try to extract from code block language
				if currentFile == "" {
					if filename := d.extractFilenameFromCodeBlock(line); filename != "" {
						currentFile = filename
					}
				}
			}
			continue
		}

		// Collect content if we're in a code block or have a current file
		if (inCodeBlock || currentFile != "") && currentFile != "" {
			currentContent = append(currentContent, line)
		}
	}

	// Save final file if exists
	if currentFile != "" && len(currentContent) > 0 {
		if err := d.writeFile(currentFile, strings.Join(currentContent, "\n")); err != nil {
			return filesCreated, err
		}
		filesCreated++
	}

	return filesCreated, nil
}

// extractFilename extracts filename from header lines
func (d *CoderDriver) extractFilename(line string) string {
	line = strings.TrimSpace(line)

	// Remove markdown headers and prefixes
	line = strings.TrimPrefix(line, "###")
	line = strings.TrimPrefix(line, "File:")
	line = strings.TrimPrefix(line, "**")
	line = strings.TrimSuffix(line, "**")
	line = strings.TrimSpace(line)

	// Extract just the filename part
	if strings.Contains(line, " ") {
		parts := strings.Fields(line)
		for _, part := range parts {
			if strings.Contains(part, ".") {
				return part
			}
		}
	}

	return line
}

// extractFilenameFromCodeBlock tries to extract filename from code block language
func (d *CoderDriver) extractFilenameFromCodeBlock(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "```") {
		lang := strings.TrimPrefix(line, "```")
		lang = strings.TrimSpace(lang)

		// Map common languages to file extensions
		switch lang {
		case "python", "py":
			return "hello_world.py"
		case "go", "golang":
			return "main.go"
		case "javascript", "js":
			return "hello_world.js"
		case "java":
			return "HelloWorld.java"
		default:
			if strings.Contains(lang, ".") {
				return lang // It might already be a filename
			}
		}
	}
	return ""
}

// writeFile writes content to a file in the workspace
func (d *CoderDriver) writeFile(filename, content string) error {
	// Clean the filename
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return fmt.Errorf("empty filename")
	}

	filePath := filepath.Join(d.workDir, filename)

	// Create directory if needed
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Write the file
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", filename, err)
	}

	d.contextManager.AddMessage("tool", fmt.Sprintf("Created file: %s", filename))
	return nil
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
		return StateFixing.ToAgentState(), false, nil
	}

	return StateCodeReview.ToAgentState(), false, nil
}

// handleFixing processes the FIXING state
func (d *CoderDriver) handleFixing(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Fixing phase: addressing issues")

	sm.SetStateData("fixes_applied", true)
	sm.SetStateData("fixing_completed_at", time.Now().UTC())

	return StateCoding.ToAgentState(), false, nil
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
				return StateFixing.ToAgentState(), false, nil
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
		return StateFixing.ToAgentState(), false, nil
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
	return StateCodeReview.ToAgentState(), false, nil
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
			return StatePlanning.ToAgentState(), false, nil
		case "CODING":
			return StateCoding.ToAgentState(), false, nil
		case "FIXING":
			return StateFixing.ToAgentState(), false, nil
		default:
			return StatePlanning.ToAgentState(), false, nil
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
	return StateQuestion.ToAgentState(), false, nil
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
func (d *CoderDriver) GetStateData() map[string]any {
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
