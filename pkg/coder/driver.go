package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
)








// State data keys - using constants to prevent key mismatch bugs
const (
	keyPlanApprovalResult = "plan_approval_result"
	keyCodeApprovalResult = "code_approval_result"
	keyArchitectAnswer    = "architect_answer"
	keyTaskContent        = "task_content"
	keyStartedAt          = "started_at"
	keyCodingIterations   = "coding_iterations"
	keyFixingIterations   = "fixing_iterations"
	
	// AUTO_CHECKIN question state keys
	keyQuestionReason     = "question_reason"
	keyQuestionOrigin     = "question_origin"
	keyQuestionContent    = "question_content"
	keyAutoCheckinAction  = "auto_checkin_action"
	keyErrorMessage       = "error_msg"
	keyLoops              = "loops"
	keyMaxLoops           = "max_loops"
	keyQuestionAnswered   = "question_answered"
	keyQuestionCompletedAt = "question_completed_at"
)

// File creation constants
const (
	defaultFilename    = "code.txt" // Standard filename for unfenced code blocks
	maxPlainBlockSize  = 50         // Maximum lines for plain content before saving as file
)

// CoderDriver implements the v2 FSM using agent foundation
type CoderDriver struct {
	*agent.BaseStateMachine // Directly embed state machine
	agentConfig             *agent.AgentConfig
	configAgent             *config.Agent
	contextManager          *contextmgr.ContextManager
	llmClient               agent.LLMClient
	renderer                *templates.Renderer
	workDir                 string
	logger                  *logx.Logger

	// Iteration budgets
	codingBudget int
	fixingBudget int

	// REQUEST→RESULT flow state
	pendingApprovalRequest *ApprovalRequest
	pendingQuestion        *Question
	
}

// ApprovalRequest represents a pending approval request
type ApprovalRequest struct {
	ID      string             // Correlation ID for tracking responses
	Content string
	Reason  string
	Type    proto.ApprovalType
}

// Question represents a pending question
type Question struct {
	ID      string // Correlation ID for tracking responses  
	Content string
	Reason  string
	Origin  string
}

// convertApprovalData converts approval data from various formats to *proto.ApprovalResult
// Handles both direct struct pointers and map[string]interface{} from JSON deserialization
func convertApprovalData(data interface{}) (*proto.ApprovalResult, error) {
	// If it's already the correct type, return it
	if result, ok := data.(*proto.ApprovalResult); ok {
		return result, nil
	}

	// If it's a map (from JSON deserialization), convert it
	if dataMap, ok := data.(map[string]interface{}); ok {
		// Convert map to JSON and then to struct
		jsonData, err := json.Marshal(dataMap)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal approval data: %w", err)
		}

		var result proto.ApprovalResult
		if err := json.Unmarshal(jsonData, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal approval data: %w", err)
		}

		return &result, nil
	}

	return nil, fmt.Errorf("unsupported approval data type: %T", data)
}

// NewCoderDriver creates a new coder driver using agent foundation
func NewCoderDriver(agentID string, stateStore *state.Store, modelConfig *config.ModelCfg, llmClient agent.LLMClient, workDir string, agentConfig *config.Agent) (*CoderDriver, error) {
	renderer, _ := templates.NewRenderer()

	// Create agent context with logger
	agentCtx := &agent.AgentContext{
		Context: context.Background(),
		Logger:  log.New(os.Stdout, fmt.Sprintf("[%s] ", agentID), log.LstdFlags),
		Store:   stateStore,
		WorkDir: workDir,
	}

	// Create agent config
	agentCfg := &agent.AgentConfig{
		ID:      agentID,
		Type:    "coder",
		Context: *agentCtx,
		LLMConfig: &agent.LLMConfig{
			MaxContextTokens: modelConfig.MaxContextTokens,
			MaxOutputTokens:  modelConfig.MaxReplyTokens,
			CompactIfOver:    modelConfig.CompactionBuffer,
		},
	}

	// Use canonical transition table from fsm package - single source of truth
	// This ensures driver behavior exactly matches STATES.md specification
	sm := agent.NewBaseStateMachine(agentID, agent.StateWaiting, stateStore, CoderTransitions)

	// Set iteration budgets from agent config, with fallback to defaults
	codingBudget := config.DefaultCodingBudget
	fixingBudget := config.DefaultFixingBudget
	if agentConfig != nil {
		if agentConfig.IterationBudgets.CodingBudget > 0 {
			codingBudget = agentConfig.IterationBudgets.CodingBudget
		}
		if agentConfig.IterationBudgets.FixingBudget > 0 {
			fixingBudget = agentConfig.IterationBudgets.FixingBudget
		}
	}

	driver := &CoderDriver{
		BaseStateMachine: sm,
		agentConfig:      agentCfg,
		configAgent:      agentConfig,
		contextManager:   contextmgr.NewContextManagerWithModel(modelConfig),
		llmClient:        llmClient,
		renderer:         renderer,
		workDir:          workDir,
		logger:           logx.NewLogger(agentID),
		codingBudget:     codingBudget,
		fixingBudget:     fixingBudget,
	}
	
	
	return driver, nil
}

// checkLoopBudget tracks loop counts and triggers AUTO_CHECKIN when budget is exceeded
// Returns true if budget exceeded and AUTO_CHECKIN should be triggered
func (d *CoderDriver) checkLoopBudget(sm *agent.BaseStateMachine, key string, budget int, origin agent.State) bool {
	// Get current iteration count
	var iterationCount int
	if val, exists := sm.GetStateValue(key); exists {
		if count, ok := val.(int); ok {
			iterationCount = count
		}
	}
	
	// Increment counter
	iterationCount++
	sm.SetStateData(key, iterationCount)
	
	// Check if budget exceeded
	if iterationCount >= budget {
		// Populate QUESTION fields for AUTO_CHECKIN
		sm.SetStateData(keyQuestionReason, QuestionReasonAutoCheckin)
		sm.SetStateData(keyQuestionOrigin, string(origin))
		sm.SetStateData(keyQuestionContent, fmt.Sprintf("Loop budget exceeded in %s state (%d/%d iterations). How should I proceed?", origin, iterationCount, budget))
		sm.SetStateData(keyLoops, iterationCount)
		sm.SetStateData(keyMaxLoops, budget)
		
		return true
	}
	
	return false
}

// ProcessState implements the v2 FSM state machine logic
func (d *CoderDriver) ProcessState(ctx context.Context) (agent.State, bool, error) {
	sm := d.BaseStateMachine

	switch d.GetCurrentState() {
	case agent.StateWaiting:
		return d.handleWaiting(ctx, sm)
	case StatePlanning:
		return d.handlePlanning(ctx, sm)
	case StatePlanReview:
		return d.handlePlanReview(ctx, sm)
	case StateCoding:
		return d.handleCoding(ctx, sm)
	case StateTesting:
		return d.handleTesting(ctx, sm)
	case StateFixing:
		return d.handleFixing(ctx, sm)
	case StateCodeReview:
		return d.handleCodeReview(ctx, sm)
	case StateQuestion:
		return d.handleQuestion(ctx, sm)
	case agent.StateDone:
		return agent.StateDone, true, nil
	case agent.StateError:
		return agent.StateError, true, nil
	default:
		return agent.StateError, false, fmt.Errorf("unknown state: %s", d.GetCurrentState())
	}
}



// isCoderState checks if a state is a coder-specific state using canonical derivation
func (d *CoderDriver) isCoderState(state agent.State) bool {
	return IsCoderState(state)
}

// ProcessTask initiates task processing with the new agent foundation
func (d *CoderDriver) ProcessTask(ctx context.Context, taskContent string) error {
	// Add agent ID to context for debug logging
	ctx = context.WithValue(ctx, "agent_id", d.agentConfig.ID)
	
	logx.DebugFlow(ctx, "coder", "task-processing", "starting", fmt.Sprintf("content=%d chars", len(taskContent)))
	
	// Reset for new task
	d.BaseStateMachine.SetStateData(keyTaskContent, taskContent)
	d.BaseStateMachine.SetStateData(keyStartedAt, time.Now().UTC())

	// Add to context manager
	d.contextManager.AddMessage("user", taskContent)

	// Initialize if needed
	if err := d.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize: %w", err)
	}

	// Run the state machine loop using Step() for atomic processing
	for {
		done, err := d.Step(ctx)
		if err != nil {
			return err
		}

		if done {
			logx.DebugFlow(ctx, "coder", "task-processing", "completed", "state machine finished")
			break
		}

		// Break out if we have pending approvals or questions to let external handler deal with them
		if d.pendingApprovalRequest != nil || d.pendingQuestion != nil {
			logx.DebugFlow(ctx, "coder", "task-processing", "paused", "pending external response")
			break
		}
	}

	return nil
}

// handleWaiting processes the WAITING state
func (d *CoderDriver) handleWaiting(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	logx.DebugState(ctx, "coder", "enter", "WAITING")
	d.contextManager.AddMessage("assistant", "Waiting for task assignment")

	taskContent, exists := sm.GetStateValue(keyTaskContent)
	if exists && taskContent != "" {
		logx.DebugState(ctx, "coder", "transition", "WAITING -> PLANNING", "task content available")
		return StatePlanning, false, nil
	}

	return agent.StateWaiting, false, nil
}

// handlePlanning processes the PLANNING state
func (d *CoderDriver) handlePlanning(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	logx.DebugState(ctx, "coder", "enter", "PLANNING")
	d.contextManager.AddMessage("assistant", "Planning phase: analyzing requirements")

	taskContent, _ := sm.GetStateValue(keyTaskContent)
	taskStr, _ := taskContent.(string)

	// Check for help requests
	if d.detectHelpRequest(taskStr) {
		sm.SetStateData(keyQuestionReason, "Help requested during planning")
		sm.SetStateData(keyQuestionContent, taskStr)
		sm.SetStateData(keyQuestionOrigin, string(StatePlanning))
		return StateQuestion, false, nil
	}

	// Generate plan
	if d.llmClient != nil {
		return d.handlePlanningWithLLM(ctx, sm, taskStr)
	}

	// Mock mode
	sm.SetStateData("plan", "Mock plan: Analyzed requirements, ready to proceed")
	sm.SetStateData("planning_completed_at", time.Now().UTC())
	return StatePlanReview, false, nil
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

	return StatePlanReview, false, nil
}

// handlePlanReview processes the PLAN_REVIEW state using REQUEST→RESULT flow
func (d *CoderDriver) handlePlanReview(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Plan review phase: requesting architect approval")

	// Check if we already have approval result
	if approvalData, exists := sm.GetStateValue(keyPlanApprovalResult); exists {
		result, err := convertApprovalData(approvalData)
		if err != nil {
			return agent.StateError, false, fmt.Errorf("failed to convert approval data: %w", err)
		}

		sm.SetStateData("plan_review_completed_at", time.Now().UTC())

		switch result.Status {
		case proto.ApprovalStatusApproved:
			return StateCoding, false, nil
		case proto.ApprovalStatusRejected, proto.ApprovalStatusNeedsChanges:
			return StatePlanning, false, nil
		default:
			return agent.StateError, false, fmt.Errorf("unknown approval status: %s", result.Status)
		}
	}

	// In mock mode (no LLM client), auto-approve
	if d.llmClient == nil {
		taskContent, _ := sm.GetStateValue(keyTaskContent)
		taskStr, _ := taskContent.(string)

		approved := d.simulateApproval(taskStr, proto.ApprovalTypePlan.String())
		result := &proto.ApprovalResult{
			Type:       proto.ApprovalTypePlan,
			Status:     proto.ApprovalStatusApproved,
			ReviewedAt: time.Now().UTC(),
		}
		if !approved {
			result.Status = proto.ApprovalStatusRejected
		}

		sm.SetStateData(keyPlanApprovalResult, result)
		sm.SetStateData("plan_review_completed_at", time.Now().UTC())

		if approved {
			return StateCoding, false, nil
		}
		return StatePlanning, false, nil
	}

	// Create approval request for architect (LLM mode)
	plan, _ := sm.GetStateValue("plan")
	planStr, _ := plan.(string)

	d.pendingApprovalRequest = &ApprovalRequest{
		ID:      proto.GenerateApprovalID(),
		Content: planStr,
		Reason:  "Plan requires architect approval before proceeding to coding",
		Type:    proto.ApprovalTypePlan,
	}

	// Stay in PLAN_REVIEW until we get approval result
	return StatePlanReview, false, nil
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

	return StateTesting, false, nil
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
		return StateTesting, false, nil
	}

	// Check iteration limit using AUTO_CHECKIN mechanism
	if d.checkLoopBudget(sm, keyCodingIterations, d.codingBudget, StateCoding) {
		log.Printf("Coding budget exceeded, triggering AUTO_CHECKIN")
		return StateQuestion, false, nil
	}

	// Add context about what's been done so far for next iteration
	fileList := d.getWorkingDirectoryContents()
	d.contextManager.AddMessage("system", fmt.Sprintf("Previous iteration created %d files/directories. Current workspace contains: %s. The implementation is not yet complete. Please continue with the next steps to create the actual source code files (like main.go, handlers, etc).", filesCreated, fileList))

	// Continue coding if implementation is not complete
	currentIterations, _ := sm.GetStateValue(keyCodingIterations)
	iterCount, _ := currentIterations.(int)
	log.Printf("Implementation appears incomplete (iteration %d/%d), continuing in CODING state", iterCount, d.codingBudget)
	
	// Note: Looping back to CODING is allowed via self-loops; not listed in CoderTransitions by design
	return StateCoding, false, nil
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

// isFilenameHeader checks if a line contains a filename header
func (d *CoderDriver) isFilenameHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "###") ||
		strings.HasPrefix(trimmed, "File:") ||
		strings.HasPrefix(trimmed, "**") ||
		strings.HasPrefix(trimmed, "=== ") ||
		strings.HasPrefix(trimmed, "--- ")
}

// looksLikeCode uses heuristics to determine if a line looks like code
func (d *CoderDriver) looksLikeCode(line string) bool {
	trimmed := strings.TrimSpace(line)
	
	// Empty lines are neutral
	if trimmed == "" {
		return false
	}
	
	// Comments and documentation are code
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || 
	   strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "<!--") {
		return true
	}
	
	// Programming language keywords and patterns
	codeKeywords := []string{
		"func ", "function ", "def ", "class ", "interface ", "struct ",
		"import ", "from ", "package ", "using ", "include ",
		"if (", "if(", "for (", "for(", "while (", "while(",
		"return ", "var ", "let ", "const ", "type ",
		"public ", "private ", "protected ", "static ",
		"async ", "await ", "yield ", "defer ",
		"console.", "fmt.", "print(", "println(", "printf(",
		".test(", ".call(", ".apply(", ".bind(",
	}
	
	for _, keyword := range codeKeywords {
		if strings.Contains(trimmed, keyword) {
			return true
		}
	}
	
	// Code-like patterns and symbols
	if strings.Contains(trimmed, "{") || strings.Contains(trimmed, "}") ||
	   strings.Contains(trimmed, "[]") || strings.Contains(trimmed, "();") ||
	   strings.Contains(trimmed, ":=") || strings.Contains(trimmed, "->") ||
	   strings.Contains(trimmed, "=>") || strings.Contains(trimmed, "<-") ||
	   strings.Contains(trimmed, "()") || strings.Contains(trimmed, "[]") ||
	   trimmed == ")" || trimmed == "(" || trimmed == "}" || trimmed == "{" ||
	   strings.Contains(trimmed, " = ") || strings.Contains(trimmed, "==") ||
	   strings.Contains(trimmed, "!=") || strings.Contains(trimmed, ">=") ||
	   strings.Contains(trimmed, "<=") || strings.Contains(trimmed, "&&") ||
	   strings.Contains(trimmed, "||") {
		return true
	}
	
	// Function calls, method calls (contains dots and parentheses)
	if strings.Contains(trimmed, ".") && strings.Contains(trimmed, "(") {
		return true
	}
	
	// String literals and numeric literals
	if strings.HasPrefix(trimmed, "\"") || strings.HasPrefix(trimmed, "'") ||
	   strings.HasPrefix(trimmed, "`") {
		return true
	}
	
	// Indentation suggests code structure
	if len(line) > len(trimmed) && (len(line)-len(trimmed)) >= 2 {
		return true
	}
	
	// Natural language patterns that are definitely NOT code
	nonCodePatterns := []string{
		"Here's", "This creates", "The following", "Now let's", "Next,", 
		"Finally,", "In this", "We will", "You can", "Let me",
		"This is", "This will", "The code", "The solution", "As you can see",
	}
	
	for _, pattern := range nonCodePatterns {
		if strings.HasPrefix(trimmed, pattern) {
			return false
		}
	}
	
	return false
}

// guessFilenameFromContent tries to guess filename from a line of code
func (d *CoderDriver) guessFilenameFromContent(line string) string {
	trimmed := strings.TrimSpace(line)
	
	// Go patterns
	if strings.HasPrefix(trimmed, "package ") || strings.Contains(trimmed, "func ") ||
	   strings.Contains(trimmed, ":=") || strings.Contains(trimmed, "fmt.") {
		return "main.go"
	}
	
	// Python patterns
	if strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "class ") ||
	   strings.Contains(trimmed, "import ") || strings.Contains(trimmed, "print(") {
		return "main.py"
	}
	
	// JavaScript patterns
	if strings.Contains(trimmed, "function ") || strings.Contains(trimmed, "const ") ||
	   strings.Contains(trimmed, "let ") || strings.Contains(trimmed, "console.") ||
	   strings.Contains(trimmed, "var ") || strings.Contains(trimmed, ".test(") ||
	   strings.Contains(trimmed, "return ") && strings.Contains(trimmed, ".") {
		return "main.js"
	}
	
	// Java patterns
	if strings.Contains(trimmed, "public class ") || strings.Contains(trimmed, "public static") {
		return "Main.java"
	}
	
	// Default
	return defaultFilename
}

// guessFilenameFromContext looks ahead in lines to guess appropriate filename
func (d *CoderDriver) guessFilenameFromContext(lines []string, startIdx int) string {
	// Look at next few lines for language clues
	for i := startIdx; i < startIdx+10 && i < len(lines); i++ {
		if filename := d.guessFilenameFromContent(lines[i]); filename != defaultFilename {
			return filename
		}
	}
	return defaultFilename
}

// parseAndCreateFiles extracts code blocks from LLM response and creates files
// Supports fenced code blocks (```), plain code blocks, and content detection
func (d *CoderDriver) parseAndCreateFiles(content string) (int, error) {
	filesCreated := 0
	lines := strings.Split(content, "\n")

	var currentFile string
	var currentContent []string
	inCodeBlock := false
	inPlainContent := false // Track when we're collecting plain content that looks like code

	for i, line := range lines {
		// Look for filename patterns like "### filename.py" or "File: filename.py"
		if d.isFilenameHeader(line) {
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
			inPlainContent = false
			continue
		}

		// Handle fenced code blocks (``` with or without language)
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inCodeBlock {
				// End of code block - save current file if it exists
				if currentFile != "" && len(currentContent) > 0 {
					if err := d.writeFile(currentFile, strings.Join(currentContent, "\n")); err != nil {
						return filesCreated, err
					}
					filesCreated++
				}
				// Reset state for next potential file
				inCodeBlock = false
				inPlainContent = false
				currentFile = ""
				currentContent = []string{}
			} else {
				// Start of code block
				inCodeBlock = true
				inPlainContent = false
				// If no current file, try to extract from code block language or guess
				if currentFile == "" {
					if filename := d.extractFilenameFromCodeBlock(line); filename != "" {
						currentFile = filename
					} else {
						// Plain code block without language - try to guess from upcoming content
						currentFile = d.guessFilenameFromContext(lines, i+1)
					}
				}
			}
			continue
		}

		// If we're not in a code block and have no current file, check if this looks like code
		if !inCodeBlock && !inPlainContent && currentFile == "" {
			if d.looksLikeCode(line) {
				// Start collecting plain content that looks like code
				inPlainContent = true
				currentFile = d.guessFilenameFromContent(line)
				currentContent = []string{}
			}
		}

		// Stop collecting plain content if we hit non-code-like lines (but allow empty lines)
		if inPlainContent && !inCodeBlock && !d.looksLikeCode(line) && strings.TrimSpace(line) != "" {
			// Check if this line looks like natural language (definitely not code)
			trimmed := strings.TrimSpace(line)
			isNaturalLanguage := false
			
			// Natural language patterns that end code blocks
			endPatterns := []string{
				"This creates", "This will", "This is", "Here's", "The following",
				"Now let's", "Next,", "Finally,", "As you can see", "Note that",
				"Remember to", "Don't forget", "Make sure", "Be careful",
			}
			
			for _, pattern := range endPatterns {
				if strings.HasPrefix(trimmed, pattern) {
					isNaturalLanguage = true
					break
				}
			}
			
			// Only stop if it's clearly natural language
			if isNaturalLanguage {
				// If we have collected some content, save it
				if currentFile != "" && len(currentContent) > 0 {
					if err := d.writeFile(currentFile, strings.Join(currentContent, "\n")); err != nil {
						return filesCreated, err
					}
					filesCreated++
				}
				currentFile = ""
				currentContent = []string{}
				inPlainContent = false
			}
		}

		// Collect content if we're in a code block, have a current file, or collecting plain content
		if (inCodeBlock || inPlainContent || currentFile != "") && currentFile != "" {
			currentContent = append(currentContent, line)
			
			// Check if we've exceeded the maximum plain block size
			if inPlainContent && len(currentContent) > maxPlainBlockSize {
				// Force save as default filename and reset
				if err := d.writeFile(defaultFilename, strings.Join(currentContent, "\n")); err != nil {
					return filesCreated, err
				}
				filesCreated++
				currentFile = ""
				currentContent = []string{}
				inPlainContent = false
			}
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
		return StateFixing, false, nil
	}

	return StateCodeReview, false, nil
}

// handleFixing processes the FIXING state
func (d *CoderDriver) handleFixing(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Fixing phase: addressing issues")

	// Check iteration limit using AUTO_CHECKIN mechanism
	if d.checkLoopBudget(sm, keyFixingIterations, d.fixingBudget, StateFixing) {
		log.Printf("Fixing budget exceeded, triggering AUTO_CHECKIN")
		return StateQuestion, false, nil
	}

	sm.SetStateData("fixes_applied", true)
	sm.SetStateData("fixing_completed_at", time.Now().UTC())

	// According to canonical FSM, FIXING should transition to TESTING, not CODING
	return StateTesting, false, nil
}

// handleCodeReview processes the CODE_REVIEW state using REQUEST→RESULT flow
func (d *CoderDriver) handleCodeReview(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Code review phase: requesting architect approval")

	// Check if we already have approval result
	if approvalData, exists := sm.GetStateValue(keyCodeApprovalResult); exists {
		result, err := convertApprovalData(approvalData)
		if err != nil {
			return agent.StateError, false, fmt.Errorf("failed to convert approval data: %w", err)
		}

		sm.SetStateData("code_review_completed_at", time.Now().UTC())

		switch result.Status {
		case proto.ApprovalStatusApproved:
			return agent.StateDone, true, nil
		case proto.ApprovalStatusRejected, proto.ApprovalStatusNeedsChanges:
			return StateFixing, false, nil
		default:
			return agent.StateError, false, fmt.Errorf("unknown approval status: %s", result.Status)
		}
	}

	// In mock mode (no LLM client), auto-approve
	if d.llmClient == nil {
		taskContent, _ := sm.GetStateValue(keyTaskContent)
		taskStr, _ := taskContent.(string)

		approved := d.simulateApproval(taskStr, proto.ApprovalTypeCode.String())
		result := &proto.ApprovalResult{
			Type:       proto.ApprovalTypeCode,
			Status:     proto.ApprovalStatusApproved,
			ReviewedAt: time.Now().UTC(),
		}
		if !approved {
			result.Status = proto.ApprovalStatusRejected
		}

		sm.SetStateData(keyCodeApprovalResult, result)
		sm.SetStateData("code_review_completed_at", time.Now().UTC())

		if approved {
			return agent.StateDone, true, nil
		}
		return StateFixing, false, nil
	}

	// Create approval request for architect (LLM mode)
	codeGenerated, _ := sm.GetStateValue("code_generated")
	codeStr := fmt.Sprintf("Code implementation completed: %v", codeGenerated)

	d.pendingApprovalRequest = &ApprovalRequest{
		ID:      proto.GenerateApprovalID(),
		Content: codeStr,
		Reason:  "Code requires architect approval before completion",
		Type:    proto.ApprovalTypeCode,
	}

	// Stay in CODE_REVIEW until we get approval result
	return StateCodeReview, false, nil
}

// handleQuestion processes the QUESTION state with origin tracking
func (d *CoderDriver) handleQuestion(ctx context.Context, sm *agent.BaseStateMachine) (agent.State, bool, error) {
	d.contextManager.AddMessage("assistant", "Question phase: awaiting clarification")

	// Check if we have an answer
	if answer, exists := sm.GetStateValue(keyArchitectAnswer); exists {
		answerStr, _ := answer.(string)
		sm.SetStateData(keyQuestionAnswered, true)
		sm.SetStateData("architect_response", answerStr)
		sm.SetStateData(keyQuestionCompletedAt, time.Now().UTC())

		// Clear the answer so we don't loop
		sm.SetStateData(keyArchitectAnswer, "")

		// Check for AUTO_CHECKIN action flags first
		if action, exists := sm.GetStateValue(keyAutoCheckinAction); exists {
			actionStr, _ := action.(string)
			// Parse the stored action string back to typed enum
			if parsedAction, err := ParseAutoAction(actionStr); err == nil {
				switch parsedAction {
				case AutoEscalate:
					return StateCodeReview, false, nil
				case AutoAbandon:
					return agent.StateError, false, fmt.Errorf("task abandoned by architect")
				}
			}
		}

		// Return to origin state using metadata
		origin, _ := sm.GetStateValue(keyQuestionOrigin)
		originStr, _ := origin.(string)

		switch originStr {
		case string(StatePlanning):
			return StatePlanning, false, nil
		case string(StateCoding):
			return StateCoding, false, nil
		case string(StateFixing):
			return StateFixing, false, nil
		// QUESTION can also transition to PLAN_REVIEW per canonical FSM
		case string(StatePlanReview):
			return StatePlanReview, false, nil
		default:
			return StatePlanning, false, nil
		}
	}

	// Create question for architect if we don't have one pending
	if d.pendingQuestion == nil {
		questionContent, _ := sm.GetStateValue(keyQuestionContent)
		questionReason, _ := sm.GetStateValue(keyQuestionReason)
		questionOrigin, _ := sm.GetStateValue(keyQuestionOrigin)
		errorMsg, _ := sm.GetStateValue(keyErrorMessage)

		// Include error message in content if present
		content := ""
		if questionContent != nil {
			content = questionContent.(string)
		}
		if errorMsg != nil && errorMsg.(string) != "" {
			if content != "" {
				content += "\n\nError: " + errorMsg.(string)
			} else {
				content = "Error: " + errorMsg.(string)
			}
		}

		d.pendingQuestion = &Question{
			ID:      proto.GenerateQuestionID(),
			Content: content,
			Reason:  questionReason.(string),
			Origin:  questionOrigin.(string),
		}
	}

	// Stay in QUESTION state until we get an answer
	return StateQuestion, false, nil
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
func (d *CoderDriver) GetPendingApprovalRequest() (bool, string, string, string, proto.ApprovalType) {
	if d.pendingApprovalRequest == nil {
		return false, "", "", "", ""
	}
	return true, d.pendingApprovalRequest.ID, d.pendingApprovalRequest.Content, d.pendingApprovalRequest.Reason, d.pendingApprovalRequest.Type
}

// ClearPendingApprovalRequest clears the pending approval request
func (d *CoderDriver) ClearPendingApprovalRequest() {
	d.pendingApprovalRequest = nil
}

// GetPendingQuestion returns pending question if any  
func (d *CoderDriver) GetPendingQuestion() (bool, string, string, string) {
	if d.pendingQuestion == nil {
		return false, "", "", ""
	}
	return true, d.pendingQuestion.ID, d.pendingQuestion.Content, d.pendingQuestion.Reason
}

// ClearPendingQuestion clears the pending question
func (d *CoderDriver) ClearPendingQuestion() {
	d.pendingQuestion = nil
}

// ProcessApprovalResult processes approval result from architect
func (d *CoderDriver) ProcessApprovalResult(approvalStatus string, approvalType string) error {
	// Convert legacy status to standardized format
	standardStatus := proto.ConvertLegacyStatus(approvalStatus)
	
	// Validate approval type
	stdApprovalType, valid := proto.ValidateApprovalType(approvalType)
	if !valid {
		return fmt.Errorf("invalid approval type: %s", approvalType)
	}

	result := &proto.ApprovalResult{
		Type:       stdApprovalType,
		Status:     standardStatus,
		ReviewedAt: time.Now().UTC(),
	}

	// Store using the correct key based on type
	switch stdApprovalType {
	case proto.ApprovalTypePlan:
		d.BaseStateMachine.SetStateData(keyPlanApprovalResult, result)
	case proto.ApprovalTypeCode:
		d.BaseStateMachine.SetStateData(keyCodeApprovalResult, result)
	default:
		return fmt.Errorf("unknown approval type: %s", approvalType)
	}

	// Persist state to ensure approval result is saved
	if err := d.BaseStateMachine.Persist(); err != nil {
		return fmt.Errorf("failed to persist approval result: %w", err)
	}
	
	// Debug logging for approval processing
	logx.DebugToFile(context.Background(), "coder", "approval_debug.log", "ProcessApprovalResult called - status=%s->%s, type=%s", approvalStatus, standardStatus, approvalType)
	
	return nil
}

// ProcessAnswer processes answer from architect
func (d *CoderDriver) ProcessAnswer(answer string) error {
	sm := d.BaseStateMachine
	
	// Check if this is an AUTO_CHECKIN response
	if reason, exists := sm.GetStateValue(keyQuestionReason); exists {
		if reasonStr, ok := reason.(string); ok && reasonStr == QuestionReasonAutoCheckin {
			return d.processAutoCheckinAnswer(answer)
		}
	}
	
	// Regular answer processing
	d.BaseStateMachine.SetStateData(keyArchitectAnswer, answer)
	return nil
}

// processAutoCheckinAnswer handles architect replies to AUTO_CHECKIN questions
func (d *CoderDriver) processAutoCheckinAnswer(answer string) error {
	sm := d.BaseStateMachine
	answer = strings.TrimSpace(answer)
	
	// Get the origin state
	origin, exists := sm.GetStateValue(keyQuestionOrigin)
	if !exists {
		return fmt.Errorf("missing question_origin for AUTO_CHECKIN")
	}
	originStr, ok := origin.(string)
	if !ok {
		return fmt.Errorf("invalid question_origin type")
	}
	
	// Parse the command
	parts := strings.Fields(strings.ToUpper(answer))
	if len(parts) == 0 {
		return d.sendAutoCheckinError(fmt.Sprintf("Empty AUTO_CHECKIN command. Valid: CONTINUE <n>, PIVOT, ESCALATE, ABANDON."))
	}
	
	// Validate command using typed enum
	commandStr := parts[0]
	command, err := ParseAutoAction(commandStr)
	if err != nil {
		return d.sendAutoCheckinError(err.Error())
	}
	
	switch command {
	case AutoContinue:
		// Parse the number parameter
		var increase int = 0
		if len(parts) > 1 {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				increase = n
			} else {
				return d.sendAutoCheckinError(fmt.Sprintf("Invalid number in CONTINUE command: %s", parts[1]))
			}
		}
		
		// Increase budget and reset counter
		switch originStr {
		case string(StateCoding):
			d.codingBudget += increase
			sm.SetStateData(keyCodingIterations, 0)
		case string(StateFixing):
			d.fixingBudget += increase
			sm.SetStateData(keyFixingIterations, 0)
		default:
			return fmt.Errorf("unknown origin state for AUTO_CHECKIN: %s", originStr)
		}
		
		// Clear question state and set answer
		d.clearQuestionState()
		sm.SetStateData(keyArchitectAnswer, fmt.Sprintf("Budget increased by %d, counter reset", increase))
		
	case AutoPivot:
		// Reset counter, stay in current state
		switch originStr {
		case string(StateCoding):
			sm.SetStateData(keyCodingIterations, 0)
		case string(StateFixing):
			sm.SetStateData(keyFixingIterations, 0)
		default:
			return fmt.Errorf("unknown origin state for AUTO_CHECKIN: %s", originStr)
		}
		
		// Clear question state and set answer
		d.clearQuestionState()
		sm.SetStateData(keyArchitectAnswer, "Counter reset, continuing with pivot approach")
		
	case AutoEscalate:
		// Set explicit flag for escalation
		d.clearQuestionState()
		sm.SetStateData(keyAutoCheckinAction, AutoEscalate.String())
		sm.SetStateData(keyArchitectAnswer, "Escalating to code review")
		
	case AutoAbandon:
		// Set explicit flag for abandonment
		d.clearQuestionState()
		sm.SetStateData(keyAutoCheckinAction, AutoAbandon.String())
		sm.SetStateData(keyArchitectAnswer, "Task abandoned")
		
	default:
		return d.sendAutoCheckinError(fmt.Sprintf("Unrecognised AUTO_CHECKIN command: %q. Valid: CONTINUE <n>, PIVOT, ESCALATE, ABANDON.", command))
	}
	
	return nil
}

// sendAutoCheckinError sends an error message back to architect for invalid commands
func (d *CoderDriver) sendAutoCheckinError(errorMsg string) error {
	// Stay in QUESTION state by not clearing question state
	// Preserve original question context and add error message separately
	d.BaseStateMachine.SetStateData(keyErrorMessage, errorMsg)
	return fmt.Errorf("invalid AUTO_CHECKIN command, staying in QUESTION state")
}

// clearQuestionState clears AUTO_CHECKIN question state
func (d *CoderDriver) clearQuestionState() {
	sm := d.BaseStateMachine
	sm.SetStateData(keyQuestionReason, "")
	sm.SetStateData(keyQuestionOrigin, "")
	sm.SetStateData(keyQuestionContent, "")
	sm.SetStateData(keyErrorMessage, "")
	sm.SetStateData(keyAutoCheckinAction, "")
	// Note: Intentionally not clearing loops/max_loops for audit purposes
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
	// Run the state machine loop using Step()
	for {
		done, err := d.Step(ctx)
		if err != nil {
			return err
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

	// Transition to next state if different, even when done
	currentState := d.GetCurrentState()
	if nextState != currentState {
		// Transition validation is handled by base state machine
		
		if err := d.TransitionTo(ctx, nextState, nil); err != nil {
			return false, fmt.Errorf("failed to transition to state %s: %w", nextState, err)
		}
	}

	return done, nil
}

// Shutdown performs cleanup (required for Driver interface)
func (d *CoderDriver) Shutdown(ctx context.Context) error {
	return d.Persist()
}
